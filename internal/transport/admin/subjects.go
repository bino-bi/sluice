// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"
	"strings"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// handleExplain proxies queryservice.Explain. The caller supplies a
// synthetic subject (user/issuer/groups/claims) and a target table; the
// engine renders the same decision the live path would produce.
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	if s.deps.Service == nil {
		s.writeAPIError(w, r, pkgerr.New(pkgerr.CodeInternal).WithMessage("queryservice not configured"))
		return
	}
	q := r.URL.Query()
	user := q.Get("user")
	if user == "" {
		s.writeAPIError(w, r, pkgerr.Newf(pkgerr.CodeSyntax, "user query parameter required"))
		return
	}
	table := q.Get("table")
	if table == "" {
		s.writeAPIError(w, r, pkgerr.Newf(pkgerr.CodeSyntax, "table query parameter required"))
		return
	}
	ref, err := parser.ParseTableName(table)
	if err != nil {
		s.writeAPIError(w, r, pkgerr.Newf(pkgerr.CodeSyntax, "%s", err.Error()))
		return
	}
	uc := &identity.UserCtx{
		Subject: user,
		Issuer:  q.Get("issuer"),
	}
	if g := q.Get("groups"); g != "" {
		uc.Groups = strings.Split(g, ",")
	}
	if claims := q["claims"]; len(claims) > 0 {
		uc.Claims = make(map[string]any, len(claims))
		for _, kv := range claims {
			key, val, ok := strings.Cut(kv, "=")
			if ok {
				uc.Claims[key] = val
			}
		}
	}

	result, err := s.deps.Service.Explain(r.Context(), queryservice.ExplainInput{
		User:         uc,
		Table:        ref,
		SimulatedSQL: q.Get("simulated_sql"),
		Origin:       queryservice.OriginAdmin,
	})
	if err != nil {
		s.writeAPIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
