// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/transport/tlsutil"
)

// Config controls the admin listener. The MVP intentionally keeps the
// surface tiny: static-token auth + read-only handlers.
type Config struct {
	// Enabled gates the whole server. Default is off.
	Enabled bool

	// Listen is the bind address (host:port).
	Listen string

	// Token is the static admin token. Compared in constant time against
	// the Authorization header. Empty Token disables auth — useful only
	// for local development; the server logs a loud warning at boot.
	Token string

	// ReadTimeout / WriteTimeout / IdleTimeout bound the HTTP connection
	// lifecycle. Zero values fall back to 5s / 30s / 30s.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// ShutdownTimeout bounds graceful-shutdown drain. Zero → 5 s.
	ShutdownTimeout time.Duration

	// MaxBodyBytes caps any request body. Zero → 256 KiB.
	MaxBodyBytes int64

	// TLS enables HTTPS when non-nil; a ClientCA additionally requires
	// client certificates (mutual TLS).
	TLS *TLSConfig
}

// TLSConfig holds the admin listener's TLS material. A non-empty
// ClientCA enables mutual TLS. Client certificates gate the transport
// only — the bearer token is still required.
type TLSConfig struct {
	CertFile string
	KeyFile  string
	ClientCA string
}

// Deps wires the admin server dependencies.
type Deps struct {
	Service   *queryservice.Service
	Policies  *policy.Engine
	Sources   *datasource.Registry
	Audit     AuditTailer
	Catalogs  queryservice.CatalogLister
	Logger    *slog.Logger
	Reloader  Reloader
	Approvals PendingLister
}

// PendingLister returns the currently-pending approval requests. A nil
// value makes /admin/approvals respond 501.
type PendingLister interface {
	Pending() []approval.View
}

// Reloader triggers a config reload. Implementations live alongside the
// config watcher; a nil Reloader makes /admin/reload respond 501.
type Reloader interface {
	Reload(ctx context.Context) error
}

// AuditTailer returns the last N audit records. Only the file sink
// satisfies this in MVP; v1 will add Postgres/S3 tailers. A nil value
// makes /admin/audit/tail respond 501.
type AuditTailer interface {
	Tail(ctx context.Context, n int) ([]*audit.Record, error)
}

// Server is the admin HTTP server.
type Server struct {
	cfg  Config
	deps Deps
	srv  *http.Server
	lg   *slog.Logger
}

// New wires a Server. The server is not listening yet; call
// ListenAndServe to bind.
func New(cfg Config, deps Deps) *Server {
	applyDefaults(&cfg)
	lg := deps.Logger
	if lg == nil {
		lg = slog.Default()
	}
	if cfg.Token == "" && cfg.Enabled {
		lg.Warn("admin: running without an auth token — do not expose this port")
	}
	s := &Server{cfg: cfg, deps: deps, lg: lg}
	s.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	return s
}

// Handler returns the composed HTTP handler (primarily for tests).
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// Addr echoes the bind address.
func (s *Server) Addr() string {
	if s.srv == nil {
		return ""
	}
	return s.srv.Addr
}

// ListenAndServe serves until ctx is done or a fatal error occurs.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if s.cfg.TLS != nil {
		tc, err := tlsutil.Build(tlsutil.Config{
			CertFile: s.cfg.TLS.CertFile,
			KeyFile:  s.cfg.TLS.KeyFile,
			ClientCA: s.cfg.TLS.ClientCA,
		})
		if err != nil {
			return fmt.Errorf("admin: tls: %w", err)
		}
		s.srv.TLSConfig = tc
		if s.cfg.TLS.ClientCA != "" {
			s.lg.InfoContext(ctx, "admin: mTLS enabled — client certificates required",
				slog.String("client_ca", s.cfg.TLS.ClientCA))
		}
	} else {
		s.lg.WarnContext(ctx, "admin: serving plain HTTP (no TLS configured)",
			slog.String("listen", s.cfg.Listen))
	}
	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLS != nil {
			errCh <- s.srv.ListenAndServeTLS("", "")
			return
		}
		errCh <- s.srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("admin: listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	}
}

// Shutdown drains in-flight requests up to ShutdownTimeout.
func (s *Server) Shutdown(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, s.cfg.ShutdownTimeout)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func applyDefaults(cfg *Config) {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 5 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 30 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 256 << 10
	}
}
