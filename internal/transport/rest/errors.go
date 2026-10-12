// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	"net/http"

	"github.com/bino-bi/sluice/internal/telemetry"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// writeError serialises an APIError into the response. The status is
// derived from the error's code and the body carries the APIError JSON
// shape defined in pkg/errors.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	telemetry.LogInternal(r.Context(), s.lg, err)
	ae := pkgerr.FromError(err)
	if ae == nil {
		ae = pkgerr.New(pkgerr.CodeInternal)
	}
	body, mErr := json.Marshal(ae)
	if mErr != nil {
		telemetry.LogInternal(r.Context(), s.lg, mErr)
		writeInternalError(w)
		return
	}
	status := ae.Status()
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if ae.QueryID != "" {
		w.Header().Set("X-Query-Id", ae.QueryID)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeInternalError is the last-resort fallback when we cannot even
// serialise the real error. It never leaks detail.
func writeInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"internal error"}`))
}
