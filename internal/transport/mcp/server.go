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
	"github.com/bino-bi/sluice/internal/telemetry"
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

	// AllowAnonymous relaxes the Streamable HTTP fail-closed default. When
	// false (the default), every HTTP request must present a credential the
	// identifier accepts or it is rejected with 401 before reaching a tool;
	// policy default-deny is then the second line of defence. When true, an
	// unauthenticated request proceeds as anonymous.
	AllowAnonymous bool

	// Tracing wraps the Streamable HTTP handler in an otel server span.
	// Stdio needs nothing: the queryservice pipeline spans become trace
	// roots there.
	Tracing bool
}

// Deps wires the server's dependencies. Service is required. Catalogs is
// optional — supplying it enables the list_catalogs tool.
type Deps struct {
	Service    *queryservice.Service
	Identifier identity.Identifier
	Catalogs   queryservice.CatalogLister
	Logger     *slog.Logger

	// PinnedUser is the identity every stdio tool call runs as. The
	// `sluice mcp` subcommand resolves it once at startup from a static
	// credential (JWT or API key). Nil means stdio runs anonymous.
	PinnedUser *identity.UserCtx
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
	switch cfg.Transport {
	case "":
		cfg.Transport = TransportStdio
	case "http", "streamable-http", "streamablehttp":
		// Operator-friendly aliases for the canonical value.
		cfg.Transport = TransportStreamableHTTP
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
	srv.AddReceivingMiddleware(cancelWithRequest)
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
	s.lg.InfoContext(ctx, "mcp: starting stdio transport",
		"identity", pinnedIdentityLabel(s.deps.PinnedUser))
	// The pinned identity is injected into the Run context so every stdio
	// tool call resolves the same UserCtx via userFrom. The SDK preserves
	// context values down to tool handlers.
	if s.deps.PinnedUser != nil {
		ctx = identity.WithUser(ctx, s.deps.PinnedUser)
	}
	if err := s.mcp.Run(ctx, &sdkmcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp stdio: %w", err)
	}
	return nil
}

func pinnedIdentityLabel(u *identity.UserCtx) string {
	if u == nil {
		return "anonymous"
	}
	if u.Subject != "" {
		return u.Subject
	}
	return "authenticated"
}

// httpHandler builds the Streamable HTTP handler chain.
func (s *Server) httpHandler() http.Handler {
	// Stateless: the SDK connects a stateful session once, with the context
	// of the request that opened it, so every later tool call in that
	// session would run as the subject of the first request. Stateless
	// mode connects per request, and a tool runs as its own caller.
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *sdkmcp.Server { return s.mcp },
		&sdkmcp.StreamableHTTPOptions{Stateless: true},
	)
	mux := http.NewServeMux()
	// Authenticate every HTTP request before it reaches the SDK handler:
	// an expired or revoked credential gets 401, not an anonymous call.
	mux.Handle("/", s.authMiddleware(withRequestCtx(handler)))

	var h http.Handler = mux
	if s.cfg.Tracing {
		h = telemetry.HTTPMiddleware("mcp")(h)
	}
	return h
}

type (
	requestCtxKey struct{}
	remoteIPKey   struct{}
)

// withRequestCtx stores the request context and the address of the client
// as context values. The SDK detaches the call context from the request
// but keeps its values.
func withRequestCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestCtxKey{}, r.Context())
		ctx = context.WithValue(ctx, remoteIPKey{}, identity.RemoteIP(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// cancelWithRequest ends a call when the HTTP request that carries it ends.
// In stateless mode a notifications/cancelled message arrives on another
// connection and cannot stop the call. Stdio has no request context.
func cancelWithRequest(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		rc, ok := ctx.Value(requestCtxKey{}).(context.Context)
		if !ok {
			return next(ctx, method, req)
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(rc, cancel)()
		return next(ctx, method, req)
	}
}

func (s *Server) runStreamable(ctx context.Context) error {
	s.http = &http.Server{
		Addr:              s.cfg.HTTPListen,
		Handler:           s.httpHandler(),
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
