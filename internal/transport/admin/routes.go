// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"

	"github.com/bino-bi/sluice/internal/telemetry"
	"github.com/bino-bi/sluice/internal/transport/httpmw"
)

// routes composes the admin router. Every protected handler lives behind
// adminAuth. Middleware order: request-id → body cap → panic recovery →
// auth → handler.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /admin/policies", s.adminAuth(http.HandlerFunc(s.handlePolicies)))
	mux.Handle("GET /admin/datasources", s.adminAuth(http.HandlerFunc(s.handleDataSources)))
	mux.Handle("GET /admin/subjects/explain", s.adminAuth(http.HandlerFunc(s.handleExplain)))
	mux.Handle("POST /admin/reload", s.adminAuth(http.HandlerFunc(s.handleReload)))
	mux.Handle("GET /admin/audit/tail", s.adminAuth(http.HandlerFunc(s.handleAuditTail)))
	mux.Handle("POST /admin/synthetic/{name}/build", s.adminAuth(http.HandlerFunc(s.handleSynthBuild)))
	mux.Handle("POST /admin/synthetic/{name}/cancel", s.adminAuth(http.HandlerFunc(s.handleSynthCancel)))
	mux.Handle("GET /admin/synthetic/{name}", s.adminAuth(http.HandlerFunc(s.handleSynthStatus)))
	mux.Handle("GET /admin/approvals", s.adminAuth(http.HandlerFunc(s.handleApprovals)))
	mux.Handle("GET /admin/healthz", s.adminAuth(http.HandlerFunc(s.handleHealthz)))
	mux.Handle("GET /admin/version", s.adminAuth(http.HandlerFunc(s.handleVersion)))
	mux.Handle("GET /metrics", s.adminAuth(telemetry.MetricsHandler()))

	var h http.Handler = mux
	h = httpmw.Recover("admin", s.lg, writeInternalError)(h)
	h = httpmw.BodyCap(s.cfg.MaxBodyBytes)(h)
	h = httpmw.RequestID("X-Admin-Request-Id")(h)
	return h
}

// writeInternalError is the response to a recovered panic.
func writeInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"internal error"}`))
}
