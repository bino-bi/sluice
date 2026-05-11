// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// handleQuery is POST /v1/query. It decodes the body, runs the full
// pipeline via queryservice, and streams the result per the negotiated
// format.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	user, _ := identity.FromContext(r.Context())

	var req apitypes.QueryRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if stderrors.As(err, &maxErr) {
			writeError(w, r, pkgerr.New(pkgerr.CodePayloadTooLarge).
				WithMessage("request body exceeds limit"))
			return
		}
		writeError(w, r, pkgerr.Newf(pkgerr.CodeSyntax, "invalid JSON body: %s", err.Error()))
		return
	}
	// Drain any trailing bytes to let keep-alive reuse the connection.
	_, _ = io.Copy(io.Discard, r.Body)

	format, err := negotiateFormat(r, req.Format)
	if err != nil {
		writeError(w, r, err)
		return
	}

	qreq := queryservice.QueryRequest{
		SQL:      req.SQL,
		Params:   req.Params,
		MaxRows:  int64(req.MaxRows),
		Timeout:  time.Duration(req.TimeoutMS) * time.Millisecond,
		Format:   format,
		User:     user,
		Origin:   queryservice.OriginREST,
		Metadata: req.Meta,
	}
	res, execErr := s.deps.Service.Execute(r.Context(), qreq)
	if execErr != nil {
		writeError(w, r, execErr)
		return
	}
	defer func() { _ = res.Rows.Close() }()

	w.Header().Set("X-Query-Id", res.QueryID)
	if len(res.Applied) > 0 {
		w.Header().Set("X-Sluice-Applied-Policies", joinAppliedPolicies(res.Applied))
	}

	switch format {
	case executor.FormatJSON:
		if err := renderJSON(w, res); err != nil {
			s.lg.WarnContext(r.Context(), "rest: json render failed",
				"query_id", res.QueryID, "error", err)
		}
	case executor.FormatCSV:
		if err := renderCSV(w, res); err != nil {
			s.lg.WarnContext(r.Context(), "rest: csv render failed",
				"query_id", res.QueryID, "error", err)
		}
	default:
		// Unreachable because negotiateFormat has already filtered.
		writeError(w, r, pkgerr.New(pkgerr.CodeInternal).
			WithMessage("unsupported output format"))
	}
}

// negotiateFormat resolves the response encoding. The request body's
// format field wins; absent that we inspect Accept. Unknown formats or
// Arrow requests (no executor support yet) yield ERR_UNSUPPORTED_SYNTAX.
func negotiateFormat(r *http.Request, bodyFormat apitypes.ResponseFormat) (executor.OutputFormat, error) {
	if bodyFormat != "" {
		switch bodyFormat {
		case apitypes.FormatJSON:
			return executor.FormatJSON, nil
		case apitypes.FormatCSV:
			return executor.FormatCSV, nil
		case apitypes.FormatArrow:
			return "", pkgerr.Newf(pkgerr.CodeUnsupportedSyntax,
				"arrow output not yet supported")
		default:
			return "", pkgerr.Newf(pkgerr.CodeSyntax,
				"unknown format %q", bodyFormat)
		}
	}
	accept := acceptHeader(r)
	switch {
	case accept == "" || strings.Contains(accept, "application/json") ||
		strings.Contains(accept, "*/*"):
		return executor.FormatJSON, nil
	case strings.Contains(accept, "text/csv"):
		return executor.FormatCSV, nil
	case strings.Contains(accept, "application/vnd.apache.arrow.stream"):
		return "", pkgerr.Newf(pkgerr.CodeUnsupportedSyntax,
			"arrow output not yet supported")
	}
	return "", pkgerr.Newf(pkgerr.CodeSyntax, "unsupported Accept header: %s", accept)
}

func joinAppliedPolicies(applied []apitypes.AppliedPolicy) string {
	parts := make([]string, 0, len(applied))
	for _, a := range applied {
		parts = append(parts, a.Name)
	}
	return strings.Join(parts, ",")
}
