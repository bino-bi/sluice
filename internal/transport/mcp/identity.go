// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"net/http"

	"github.com/bino-bi/sluice/internal/identity"
)

// authMiddleware authenticates every Streamable HTTP request before it
// reaches the SDK handler and injects the resulting UserCtx onto the
// request context so tool handlers resolve it via userFrom.
//
// Posture: fail-closed by default. A request that presents no credential is
// allowed through as anonymous only when Config.AllowAnonymous is set;
// a request that presents an *invalid* credential (bad signature, expired,
// unknown key) is always rejected with 401, matching the REST data plane.
// Running auth on every request — not just the session-init request — means
// a leaked Mcp-Session-Id cannot be replayed without the credential and a
// token's expiry is enforced on each call.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Identifier == nil {
			next.ServeHTTP(w, r)
			return
		}
		u, err := s.deps.Identifier.Identify(r.Context(), r)
		if err != nil {
			if s.cfg.AllowAnonymous && errors.Is(err, identity.ErrNoCredential) {
				next.ServeHTTP(w, r)
				return
			}
			s.lg.DebugContext(r.Context(), "mcp: identify failed", "error", err)
			w.Header().Set("WWW-Authenticate", `Bearer realm="sluice-mcp"`)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"ERR_UNAUTHORIZED","message":"authentication required"}`))
			return
		}
		r = r.WithContext(identity.WithUser(r.Context(), u))
		next.ServeHTTP(w, r)
	})
}

// userFrom fetches the UserCtx from ctx. For stdio flows the composition
// root (`sluice mcp`) pins a UserCtx onto the Run context at startup; for
// Streamable HTTP authMiddleware injects it per request.
func userFrom(ctx context.Context) (*identity.UserCtx, bool) {
	return identity.FromContext(ctx)
}
