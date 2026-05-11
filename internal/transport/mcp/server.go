// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/version"
)

// TransportMode names the wire the server listens on.
type TransportMode string

// Transport modes.
const (
	TransportStdio          TransportMode = "stdio"
	TransportStreamableHTTP TransportMode = "streamable_http"
)

// Config controls the MCP server. Enabled=false is the MVP-safe default —
// the server is opt-in.
type Config struct {
	Enabled bool

	// Transport selects the wire. Defaults to TransportStdio.
	Transport TransportMode

	// HTTPListen is the bind address when Transport = TransportStreamableHTTP.
	HTTPListen string

	// SessionIdleMax bounds the Streamable HTTP idle timeout. Zero falls
	// back to 10 minutes.
	SessionIdleMax time.Duration

	// ReadTimeout / WriteTimeout bound the Streamable HTTP server. Zero
	// values fall back to 30 s / 60 s.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Deps wires the server's dependencies. Service is required. Catalogs is
// optional — supplying it enables the list_catalogs tool.
type Deps struct {
	Service    *queryservice.Service
	Identifier identity.Identifier
	Catalogs   queryservice.CatalogLister
	Logger     *slog.Logger
}

// Server wraps sdkmcp.Server and optional HTTP listener.
type Server struct {
	cfg  Config
	deps Deps
	mcp  *sdkmcp.Server
	http *http.Server
	lg   *slog.Logger
}

// New constructs and configures the MCP server with the four MVP tools.
// Call Run to start serving.
func New(cfg Config, deps Deps) (*Server, error) {
	if deps.Service == nil {
		return nil, errors.New("mcp: Deps.Service is required")
	}
	if cfg.Transport == "" {
		cfg.Transport = TransportStdio
	}
	if cfg.SessionIdleMax <= 0 {
		cfg.SessionIdleMax = 10 * time.Minute
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 60 * time.Second
	}
	lg := deps.Logger
	if lg == nil {
		lg = slog.Default()
	}
	impl := &sdkmcp.Implementation{
		Name:    "sluice",
		Title:   "Sluice — policy-enforcing SQL gateway",
		Version: version.Current().Version,
	}
	srv := sdkmcp.NewServer(impl, nil)
	s := &Server{cfg: cfg, deps: deps, mcp: srv, lg: lg}
	s.registerTools()
	return s, nil
}

// Run starts the configured transport and blocks until ctx is cancelled.
// Both stdio and Streamable HTTP honour graceful shutdown on ctx done.
func (s *Server) Run(ctx context.Context) error {
	switch s.cfg.Transport {
	case TransportStdio:
		return s.runStdio(ctx)
	case TransportStreamableHTTP:
		return s.runStreamable(ctx)
	default:
		return fmt.Errorf("mcp: unknown transport %q", s.cfg.Transport)
	}
}

// Shutdown terminates the Streamable HTTP server if one is running. Stdio
// closes implicitly when Run returns.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) runStdio(ctx context.Context) error {
	s.lg.InfoContext(ctx, "mcp: starting stdio transport")
	if err := s.mcp.Run(ctx, &sdkmcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp stdio: %w", err)
	}
	return nil
}

func (s *Server) runStreamable(ctx context.Context) error {
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(r *http.Request) *sdkmcp.Server {
			// Bridge the HTTP request into the identity pipeline and stash
			// the resulting UserCtx on the request context so the tool
			// handlers can reach it.
			_ = s.authenticateHTTP(r) // context is mutated inside
			return s.mcp
		},
		nil,
	)
	mux := http.NewServeMux()
	mux.Handle("/", handler)

	s.http = &http.Server{
		Addr:              s.cfg.HTTPListen,
		Handler:           mux,
		ReadTimeout:       s.cfg.ReadTimeout,
		ReadHeaderTimeout: s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.SessionIdleMax,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.http.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("mcp streamable: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	}
}
