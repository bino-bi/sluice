// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/queryservice"
)

// Config controls the listener, timeouts, and body limits.
type Config struct {
	// Listen is the bind address (host:port). Required.
	Listen string

	// TLS enables HTTPS when non-nil.
	TLS *TLSConfig

	// ReadTimeout, WriteTimeout, IdleTimeout bound the HTTP connection
	// lifecycle. Zero values fall back to MVP defaults (5 s / 60 s / 60 s).
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// MaxBodyBytes caps the /v1/query body. Zero falls back to 1 MiB.
	MaxBodyBytes int64

	// RequestTimeout caps overall request handling (includes queryservice
	// execution). Zero falls back to 60 s.
	RequestTimeout time.Duration

	// ShutdownTimeout bounds graceful-shutdown drain. Zero falls back to
	// 10 s.
	ShutdownTimeout time.Duration
}

// TLSConfig holds the cert/key pair for HTTPS. MVP supports a single
// server cert; mTLS lands in v1.
type TLSConfig struct {
	CertFile string
	KeyFile  string
}

// Deps wires the request pipeline dependencies. All non-nil fields are
// required except Logger.
type Deps struct {
	// Service is the queryservice orchestrator shared across transports.
	Service *queryservice.Service

	// Identifier is the identity composite (JWT + API-key + …).
	Identifier identity.Identifier

	// Registry exposes datasource health + schema state for /v1/ready.
	// Nil disables data-source readiness probing (liveness still works).
	Registry *datasource.Registry

	// Logger receives request/error logs. Nil uses slog.Default.
	Logger *slog.Logger
}

// Server wraps an *http.Server with the chi-like routing and graceful
// shutdown semantics transports share.
type Server struct {
	cfg  Config
	deps Deps
	srv  *http.Server
	lg   *slog.Logger
}

// New constructs a Server but does not start listening. Call
// ListenAndServe to bind.
func New(cfg Config, deps Deps) *Server {
	applyDefaults(&cfg)
	lg := deps.Logger
	if lg == nil {
		lg = slog.Default()
	}
	s := &Server{cfg: cfg, deps: deps, lg: lg}
	s.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.Background() },
	}
	return s
}

// Addr reports the server's bind address. Useful in tests that let the
// kernel choose a free port (:0) — call after ListenAndServe has
// populated the listener.
func (s *Server) Addr() string {
	if s.srv == nil {
		return ""
	}
	return s.srv.Addr
}

// Handler returns the composed HTTP handler. Intended for tests that use
// httptest.NewServer rather than ListenAndServe.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// ListenAndServe binds and serves until ctx is cancelled or a fatal error
// occurs. A cancelled ctx triggers graceful shutdown honouring
// Config.ShutdownTimeout.
func (s *Server) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if s.cfg.TLS != nil {
			errCh <- s.srv.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
			return
		}
		s.lg.WarnContext(ctx, "rest: serving plain HTTP (no TLS configured)",
			slog.String("listen", s.cfg.Listen))
		errCh <- s.srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("rest: listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	}
}

// Shutdown drains in-flight requests up to ShutdownTimeout, then forces
// close. Safe to call more than once.
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
		cfg.WriteTimeout = 60 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 60 * time.Second
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20 // 1 MiB
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 60 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}
}
