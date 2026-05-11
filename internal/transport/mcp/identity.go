// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"net/http"

	"github.com/bino-bi/sluice/internal/identity"
)

// authenticateHTTP runs the composite identifier over r. The resulting
// UserCtx is written back on r.Context() via WithUser so downstream tool
// handlers can reach it. Failures are swallowed here — the tool handler
// surfaces an unauthorized result when no user is present.
func (s *Server) authenticateHTTP(r *http.Request) *identity.UserCtx {
	if s.deps.Identifier == nil {
		return nil
	}
	u, err := s.deps.Identifier.Identify(r.Context(), r)
	if err != nil {
		s.lg.DebugContext(r.Context(), "mcp: identify failed", "error", err)
		return nil
	}
	ctx := identity.WithUser(r.Context(), u)
	*r = *r.WithContext(ctx)
	return u
}

// userFrom fetches the UserCtx from ctx. Stdio transports inherit the
// process environment, so for MVP stdio flows the composition root (cmd
// sluice mcp) is responsible for building a synthetic UserCtx at
// startup — either anonymous or bound to a token-derived identity.
func userFrom(ctx context.Context) (*identity.UserCtx, bool) {
	return identity.FromContext(ctx)
}
