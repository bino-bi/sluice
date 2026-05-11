// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"net/http"

	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

type policiesResponse struct {
	Version  int64                             `json:"version"`
	Digest   string                            `json:"digest"`
	Policies map[apitypes.Kind][]policySummary `json:"policies"`
	Warnings []string                          `json:"warnings,omitempty"`
}

type policySummary struct {
	Name        string                   `json:"name"`
	Priority    int32                    `json:"priority"`
	Enforcement apitypes.EnforcementMode `json:"enforcement"`
}

// handlePolicies returns the live compiled snapshot. When Policies is nil
// (compose-root not wired), the response contains an empty policy set —
// still a valid JSON document so clients can feature-detect.
func (s *Server) handlePolicies(w http.ResponseWriter, _ *http.Request) {
	resp := policiesResponse{
		Policies: map[apitypes.Kind][]policySummary{},
	}
	if s.deps.Policies != nil {
		snap := s.deps.Policies.Snapshot()
		if snap != nil {
			resp.Version = snap.Version
			resp.Digest = snap.Digest
			resp.Warnings = snap.Warnings
			for _, p := range snap.Policies {
				resp.Policies[p.Kind] = append(resp.Policies[p.Kind], policySummary{
					Name:        p.Name,
					Priority:    p.Priority,
					Enforcement: p.Enforcement,
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// Compile-time guards that policy.CompiledPolicy shape assumptions hold.
var (
	_ = policy.CompiledPolicy{}.Name
	_ = policy.CompiledPolicy{}.Priority
)
