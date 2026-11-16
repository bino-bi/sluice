// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	"net/http"

	"github.com/bino-bi/sluice/internal/version"
)

// liveResponse is the /v1/health body. Keeping it local (rather than
// reusing apitypes.HealthStatus) so health responses never depend on the
// data plane being up.
type liveResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// readyResponse carries counts only. The route needs no login, so names,
// types and driver errors stay on /admin/datasources.
type readyResponse struct {
	Status      string        `json:"status"`
	Version     string        `json:"version"`
	DataSources *dsReadyCount `json:"datasources,omitempty"`
}

type dsReadyCount struct {
	Healthy int `json:"healthy"`
	Total   int `json:"total"`
}

// handleHealth is a liveness probe. Always 200 so long as the process is
// serving.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, liveResponse{
		Status:  "ok",
		Version: version.Current().Version,
	})
}

// handleReady reports readiness. 200 when every data source is healthy;
// 503 with status degraded otherwise. When Registry is nil we only
// report the process version.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	resp := readyResponse{
		Status:  "ok",
		Version: version.Current().Version,
	}
	if s.deps.Registry != nil {
		statuses := s.deps.Registry.Statuses()
		count := &dsReadyCount{Total: len(statuses)}
		for _, st := range statuses {
			if st.Healthy {
				count.Healthy++
			}
		}
		resp.DataSources = count
		if count.Healthy < count.Total {
			resp.Status = "degraded"
			writeJSON(w, http.StatusServiceUnavailable, resp)
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
