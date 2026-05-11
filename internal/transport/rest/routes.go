// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
)

// routes composes the HTTP handler: middleware stack + route table. The
// ordering matches plan §9 (outermost → innermost).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public (no auth) endpoints.
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/ready", s.handleReady)
	mux.HandleFunc("GET /v1/version", s.handleVersion)
	mux.HandleFunc("GET /openapi.json", s.handleOpenAPI)

	// Protected data-plane endpoint.
	authMW := identity.Middleware(identity.MiddlewareOptions{
		Identifier: s.deps.Identifier,
		Logger:     s.lg,
	})
	mux.Handle("POST /v1/query", authMW(http.HandlerFunc(s.handleQuery)))

	// Outermost wrapping: request-id → body-cap → timeout → panic recovery
	// → metric tagging (placeholder until telemetry.HTTPMiddleware lands).
	var h http.Handler = mux
	h = s.panicRecovery(h)
	h = s.requestTimeout(h)
	h = s.bodyCap(h)
	h = s.requestID(h)
	return h
}

// requestID attaches an X-Request-Id response header and a ULID-ish token
// for logs. A real telemetry.HTTPMiddleware will replace this once the
// OTel initialisation slice (deferred) lands.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), requestIDKey{}, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// bodyCap rejects oversized bodies early so the transport never allocates
// more than Config.MaxBodyBytes regardless of the handler.
func (s *Server) bodyCap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.ContentLength != 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// requestTimeout caps the entire handler chain at Config.RequestTimeout.
// We use context deadlines rather than http.TimeoutHandler so streaming
// renderers can flush partial output before cancellation.
func (s *Server) requestTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// panicRecovery logs the panic and returns ERR_INTERNAL. Without this a
// panic in a handler would reset the connection and leak the stack trace.
func (s *Server) panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.lg.ErrorContext(r.Context(), "rest: panic recovered",
					"path", r.URL.Path,
					"recover", rec,
				)
				writeInternalError(w)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// newRequestID returns a short random hex token. When the telemetry
// middleware lands it will supersede this with an OTel trace ID.
func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// requestIDKey keys the request ID on the context.
type requestIDKey struct{}

// RequestIDFromContext returns the request ID attached by the middleware.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// acceptHeader returns the inspected Accept header with whitespace trimmed.
func acceptHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Accept"))
}

// Ensure the rest package's time import survives gofmt when future
// middlewares are added. The telemetry.HTTPMiddleware landing here will
// restore explicit use.
var _ = time.Second
