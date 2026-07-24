// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"errors"
	"net/http"

	"github.com/bino-bi/sluice/internal/policy"
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
		// Policy-document failures get their own code so callers can
		// tell "fix the policy file" apart from a broken config load.
		code := pkgerr.CodeConfigInvalid
		if errors.Is(err, policy.ErrSnapshotInvalid) {
			code = pkgerr.CodePolicyInvalid
		}
		writeAPIError(w, pkgerr.Wrap(code, err))
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
