// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/ratelimit"
)

// Failed admin logins per client IP: a burst of 10, then one every 10s.
// An IP over the limit gets 429 before its token is checked.
var failedAuthSpec = ratelimit.Spec{RPS: 0.1, Burst: 10}

// adminAuth is the middleware applied to every protected endpoint. It
// matches the Authorization header in constant time against Config.Token.
// An empty configured token permits anonymous access; the bootstrap only
// allows that with admin.allowAnonymous or a loopback listener.
func (s *Server) adminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		ip := identity.RemoteIP(r)
		if s.failedAuth.Exhausted(ip) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"ERR_RATE_LIMITED","message":"too many failed admin logins"}`))
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			s.failedAuth.Allow(ip)
			s.lg.WarnContext(r.Context(), "admin: auth failed",
				"remote", r.RemoteAddr, "path", r.URL.Path)
			if s.deps.AuthFailed != nil {
				reason := "invalid credential"
				if r.Header.Get("Authorization") == "" {
					reason = "no credential"
				}
				s.deps.AuthFailed(ip, reason)
			}
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
