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

type readyResponse struct {
	Status      string         `json:"status"`
	Version     string         `json:"version"`
	DataSources []dsReadyEntry `json:"datasources,omitempty"`
}

type dsReadyEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
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
// 503 with the unhealthy set otherwise. When Registry is nil we only
// report the process version.
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	resp := readyResponse{
		Status:  "ok",
		Version: version.Current().Version,
	}
	if s.deps.Registry != nil {
		statuses := s.deps.Registry.Statuses()
		unhealthy := 0
		for _, st := range statuses {
			entry := dsReadyEntry{
				Name:    st.Name,
				Type:    st.Type,
				Healthy: st.Healthy,
				Error:   st.LastError,
			}
			resp.DataSources = append(resp.DataSources, entry)
			if !st.Healthy {
				unhealthy++
			}
		}
		if unhealthy > 0 {
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
