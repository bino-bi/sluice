// SPDX-License-Identifier: AGPL-3.0-or-later

package admin

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/bino-bi/sluice/internal/config"
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

// handlePolicies lists the policy documents of the published snapshot.
// When Snapshots is nil (compose-root not wired), the response contains
// an empty policy set. It is still a valid JSON document so clients can
// feature-detect.
func (s *Server) handlePolicies(w http.ResponseWriter, _ *http.Request) {
	resp := policiesResponse{
		Policies: map[apitypes.Kind][]policySummary{},
	}
	var snap *config.Snapshot
	if s.deps.Snapshots != nil {
		snap = s.deps.Snapshots.Current()
	}
	if snap != nil {
		resp.Version = snap.Version
		resp.Digest = snap.Digest
		for _, wa := range snap.Warnings {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("%s:%d: %s", wa.File, wa.Line, wa.Msg))
		}
		for _, obj := range snap.Policies {
			enf, ok := enforcementOf(obj)
			if !ok {
				continue
			}
			meta := obj.GetObjectMeta()
			resp.Policies[obj.GetKind()] = append(resp.Policies[obj.GetKind()], policySummary{
				Name:        meta.Name,
				Priority:    meta.Priority,
				Enforcement: enf,
			})
		}
		// The order the engine evaluates in: priority, then name.
		for _, list := range resp.Policies {
			sort.SliceStable(list, func(i, j int) bool {
				if list[i].Priority != list[j].Priority {
					return list[i].Priority > list[j].Priority
				}
				return list[i].Name < list[j].Name
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// enforcementOf gives the enforcement mode of a policy document, with the
// default Enforce. ok is false for a kind that is not a policy.
func enforcementOf(obj apitypes.Object) (apitypes.EnforcementMode, bool) {
	var m apitypes.EnforcementMode
	switch p := obj.(type) {
	case *apitypes.SQLAccessPolicy:
		m = p.Spec.EnforcementMode
	case *apitypes.RowFilterPolicy:
		m = p.Spec.EnforcementMode
	case *apitypes.ColumnMaskPolicy:
		m = p.Spec.EnforcementMode
	case *apitypes.QueryRejectPolicy:
		m = p.Spec.EnforcementMode
	case *apitypes.QueryRewritePolicy:
		m = p.Spec.EnforcementMode
	case *apitypes.ApprovalPolicy:
		m = p.Spec.EnforcementMode
	default:
		return "", false
	}
	if m == "" {
		m = apitypes.EnforcementEnforce
	}
	return m, true
}
