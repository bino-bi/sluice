// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// routes composes the admin router. Every protected handler lives behind
// adminAuth. Middleware order: request-id → body cap → auth → handler.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /admin/policies", s.adminAuth(http.HandlerFunc(s.handlePolicies)))
	mux.Handle("GET /admin/datasources", s.adminAuth(http.HandlerFunc(s.handleDataSources)))
	mux.Handle("GET /admin/subjects/explain", s.adminAuth(http.HandlerFunc(s.handleExplain)))
	mux.Handle("POST /admin/reload", s.adminAuth(http.HandlerFunc(s.handleReload)))
	mux.Handle("GET /admin/audit/tail", s.adminAuth(http.HandlerFunc(s.handleAuditTail)))
	mux.Handle("GET /admin/approvals", s.adminAuth(http.HandlerFunc(s.handleApprovals)))
	mux.Handle("GET /admin/healthz", s.adminAuth(http.HandlerFunc(s.handleHealthz)))
	mux.Handle("GET /admin/version", s.adminAuth(http.HandlerFunc(s.handleVersion)))

	var h http.Handler = mux
	h = s.bodyCap(h)
	h = s.requestID(h)
	return h
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Admin-Request-Id")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Admin-Request-Id", rid)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) bodyCap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.ContentLength != 0 {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
