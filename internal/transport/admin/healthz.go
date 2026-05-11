// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"

	"github.com/bino-bi/sluice/internal/version"
)

type healthzResponse struct {
	Status       string `json:"status"`
	Version      string `json:"version"`
	ConfigDigest string `json:"config_digest,omitempty"`
	ConfigVer    int64  `json:"config_version,omitempty"`
}

// handleHealthz is an admin replica of /v1/ready. Adds the config digest
// + version for operators watching hot reloads.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	resp := healthzResponse{
		Status:  "ok",
		Version: version.Current().Version,
	}
	if s.deps.Policies != nil {
		if snap := s.deps.Policies.Snapshot(); snap != nil {
			resp.ConfigDigest = snap.Digest
			resp.ConfigVer = snap.Version
		}
	}
	status := http.StatusOK
	if s.deps.Sources != nil {
		for _, st := range s.deps.Sources.Statuses() {
			if !st.Healthy {
				resp.Status = "degraded"
				status = http.StatusServiceUnavailable
				break
			}
		}
	}
	writeJSON(w, status, resp)
}

type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	Go        string `json:"go,omitempty"`
}

// handleVersion echoes the compiled build identity.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	b := version.Current()
	writeJSON(w, http.StatusOK, versionResponse{
		Version:   b.Version,
		Commit:    b.Commit,
		BuildTime: b.BuildTime.UTC().Format("2006-01-02T15:04:05Z"),
		Go:        b.GoVersion,
	})
}
