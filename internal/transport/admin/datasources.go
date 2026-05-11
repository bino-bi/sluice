// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"
	"time"
)

type dataSourceEntry struct {
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Healthy        bool      `json:"healthy"`
	LastCheck      time.Time `json:"last_check"`
	LastError      string    `json:"last_error,omitempty"`
	LatencyMs      int64     `json:"latency_ms,omitempty"`
	SchemaPulledAt time.Time `json:"schema_pulled_at"`
}

type dataSourcesResponse struct {
	DataSources []dataSourceEntry `json:"datasources"`
}

// handleDataSources returns the status list from the datasource Registry.
// When no registry is wired (e.g. CLI-only composition) it returns an
// empty list.
func (s *Server) handleDataSources(w http.ResponseWriter, _ *http.Request) {
	resp := dataSourcesResponse{DataSources: []dataSourceEntry{}}
	if s.deps.Sources != nil {
		for _, st := range s.deps.Sources.Statuses() {
			resp.DataSources = append(resp.DataSources, dataSourceEntry{
				Name:           st.Name,
				Type:           st.Type,
				Healthy:        st.Healthy,
				LastCheck:      st.LastCheck,
				LastError:      st.LastError,
				LatencyMs:      st.LastLatency.Milliseconds(),
				SchemaPulledAt: st.SchemaPulledAt,
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
