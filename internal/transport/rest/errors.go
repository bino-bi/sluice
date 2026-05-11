// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	"net/http"

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// writeError serialises an APIError into the response. The status is
// derived from the error's code and the body carries the APIError JSON
// shape defined in pkg/errors.
func writeError(w http.ResponseWriter, _ *http.Request, err error) {
	ae := pkgerr.FromError(err)
	if ae == nil {
		ae = pkgerr.New(pkgerr.CodeInternal)
	}
	body, mErr := json.Marshal(ae)
	if mErr != nil {
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
