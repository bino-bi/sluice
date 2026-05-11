// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// handleAuditTail streams the last N audit records as NDJSON. When no
// AuditTailer is wired, responds 501 so callers know tailing isn't
// enabled by this build/configuration.
func (s *Server) handleAuditTail(w http.ResponseWriter, r *http.Request) {
	if s.deps.Audit == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"audit tail not enabled"}`))
		return
	}
	n := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAPIError(w, pkgerr.Newf(pkgerr.CodeSyntax, "invalid n=%s", raw))
			return
		}
		const maxN = 1000
		if parsed > maxN {
			parsed = maxN
		}
		n = parsed
	}
	records, err := s.deps.Audit.Tail(r.Context(), n)
	if err != nil {
		writeAPIError(w, pkgerr.Wrap(pkgerr.CodeInternal, err))
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			return
		}
	}
}

// writeAPIError serialises an APIError (or a wrapped cause) with the
// correct HTTP status pulled from pkg/errors.Status.
func writeAPIError(w http.ResponseWriter, err error) {
	ae := pkgerr.FromError(err)
	if ae == nil {
		ae = pkgerr.New(pkgerr.CodeInternal)
	}
	body, mErr := json.Marshal(ae)
	if mErr != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"admin: error encoding"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(ae.Status())
	_, _ = w.Write(body)
}
