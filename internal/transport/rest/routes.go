// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"context"
	"net/http"
	"strings"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/telemetry"
	"github.com/bino-bi/sluice/internal/transport/httpmw"
)

// routes composes the HTTP handler: middleware stack + route table. The
// ordering matches plan §9 (outermost → innermost).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public (no auth) endpoints.
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/ready", s.handleReady)
	mux.HandleFunc("GET /v1/version", s.handleVersion)

	// Protected data-plane endpoint.
	authMW := identity.Middleware(identity.MiddlewareOptions{
		Identifier: s.deps.Identifier,
		Logger:     s.lg,
		OnFailure:  s.deps.AuthFailed,
	})
	// The transport-level rate buckets run before identity on every route
	// that runs it, so a flood cannot exhaust JWKS fetches / key hashing;
	// health and approval capability endpoints stay unlimited (probes
	// must not 429).
	limit := httpmw.RateLimit(s.deps.PerIPLimiter, s.deps.GlobalLimiter, s.denyRateLimited)
	mux.Handle("POST /v1/query", limit(authMW(http.HandlerFunc(s.handleQuery))))
	mux.Handle("POST /v1/sample", limit(authMW(http.HandlerFunc(s.handleSample))))

	// Approval capability endpoints. Accept/reject are PUBLIC — the
	// capability token in the request IS the authorisation, so no identity
	// middleware. GET (and HEAD) shows a page with a form; only POST
	// decides. The status poll is authenticated: only the requesting
	// subject may read a request's state (the requester never holds the
	// capability tokens).
	if s.deps.Approvals != nil {
		mux.HandleFunc("GET /v1/approvals/{id}/accept", handleApprovalPage(true))
		mux.HandleFunc("POST /v1/approvals/{id}/accept", s.handleApprovalDecision(true))
		mux.HandleFunc("GET /v1/approvals/{id}/reject", handleApprovalPage(false))
		mux.HandleFunc("POST /v1/approvals/{id}/reject", s.handleApprovalDecision(false))
		mux.Handle("GET /v1/approvals/{id}", limit(authMW(http.HandlerFunc(s.handleApprovalStatus))))
	}

	// Outermost wrapping: request-id → body-cap → timeout → panic
	// recovery → otel span (when tracing). The otel handler sits directly
	// on the mux: ServeMux stamps Request.Pattern on the request it was
	// handed, and any WithContext clone in between would hide the pattern
	// from the span-renaming pass.
	var h http.Handler = mux
	if s.cfg.Tracing {
		h = telemetry.HTTPMiddleware("rest")(h)
	}
	h = httpmw.Recover("rest", s.lg, writeInternalError)(h)
	h = httpmw.Timeout(s.cfg.RequestTimeout)(h)
	h = httpmw.BodyCap(s.cfg.MaxBodyBytes)(h)
	h = httpmw.RequestID("X-Request-Id")(h)
	return h
}

// RequestIDFromContext returns the request ID attached by the middleware.
func RequestIDFromContext(ctx context.Context) string {
	return httpmw.RequestIDFromContext(ctx)
}

// acceptHeader returns the inspected Accept header with whitespace trimmed.
func acceptHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Accept"))
}
