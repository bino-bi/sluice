// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"net/http"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// handleSample is POST /v1/sample. queryservice builds the SQL from the
// request; the response is the same as for POST /v1/query.
func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.FromContext(r.Context())

	var req apitypes.SampleRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	format, err := negotiateFormat(r, req.Format)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.deps.Service.Sample(r.Context(), queryservice.SampleRequest{
		Table:      req.Table,
		Columns:    req.Columns,
		Rows:       req.Rows,
		Rate:       req.Rate,
		Method:     req.Method,
		StratifyBy: req.StratifyBy,
		Seed:       req.Seed,
		Where:      req.Where,
		Format:     format,
		User:       user,
		Facts:      policy.NewRequestFacts(r.Header, identity.RemoteIP(r)),
		Origin:     queryservice.OriginREST,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.serveResult(w, r, format, res)
}
