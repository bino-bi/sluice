// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// adminAuth is the middleware applied to every protected endpoint. It
// matches the Authorization header in constant time against Config.Token.
// An empty configured token permits anonymous access (dev mode); the
// boot-time warning in New makes that visible.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			s.lg.WarnContext(r.Context(), "admin: auth failed",
				"remote", r.RemoteAddr, "path", r.URL.Path)
			writeAuthError(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="sluice-admin"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"code":"ERR_UNAUTHORIZED","message":"admin auth required"}`))
}

// writeJSON serialises body as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
