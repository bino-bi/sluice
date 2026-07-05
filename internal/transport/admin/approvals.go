// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/bino-bi/sluice/internal/approval"
)

// handleApprovals lists the currently-pending approval requests. The
// response carries no capability tokens (approval.View is token-free).
// Responds 501 when no approval broker is wired.
func (s *Server) handleApprovals(w http.ResponseWriter, _ *http.Request) {
	if s.deps.Approvals == nil {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"approvals not configured"}`))
		return
	}
	pending := s.deps.Approvals.Pending()
	if pending == nil {
		pending = []approval.View{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"pending": pending, "count": len(pending)})
}
