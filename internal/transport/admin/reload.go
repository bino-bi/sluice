// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"

	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

type reloadResponse struct {
	Ok     bool   `json:"ok"`
	Digest string `json:"digest,omitempty"`
}

// handleReload triggers a config reload when a Reloader is wired. 501
// otherwise so callers know the composition root hasn't enabled it yet.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.deps.Reloader == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"reload not enabled"}`))
		return
	}
	if err := s.deps.Reloader.Reload(r.Context()); err != nil {
		writeAPIError(w, pkgerr.Wrap(pkgerr.CodeConfigInvalid, err))
		return
	}
	resp := reloadResponse{Ok: true}
	if s.deps.Policies != nil {
		if snap := s.deps.Policies.Snapshot(); snap != nil {
			resp.Digest = snap.Digest
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
