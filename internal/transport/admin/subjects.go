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
		writeAPIError(w, pkgerr.New(pkgerr.CodeInternal).WithMessage("queryservice not configured"))
		return
	}
	q := r.URL.Query()
	user := q.Get("user")
	if user == "" {
		writeAPIError(w, pkgerr.Newf(pkgerr.CodeSyntax, "user query parameter required"))
		return
	}
	table := q.Get("table")
	if table == "" {
		writeAPIError(w, pkgerr.Newf(pkgerr.CodeSyntax, "table query parameter required"))
		return
	}
	ref, err := parseTableRef(table)
	if err != nil {
		writeAPIError(w, pkgerr.Newf(pkgerr.CodeSyntax, "%s", err.Error()))
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
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// parseTableRef splits "catalog.schema.table".
func parseTableRef(s string) (parser.TableRef, error) {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 3:
		return parser.TableRef{Catalog: parts[0], Schema: parts[1], Table: parts[2]}, nil
	case 2:
		return parser.TableRef{Catalog: parts[0], Table: parts[1]}, nil
	case 1:
		return parser.TableRef{Table: parts[0]}, nil
	}
	return parser.TableRef{}, &parseTableRefError{input: s}
}

type parseTableRefError struct{ input string }

func (e *parseTableRefError) Error() string {
	return "invalid table reference: " + e.input
}
