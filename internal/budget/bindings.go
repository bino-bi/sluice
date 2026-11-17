// SPDX-License-Identifier: AGPL-3.0-or-later

package budget

import (
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// SpecsFromBindings derives per-subject and per-issuer budget specs from
// SubjectBinding.spec.budget, keyed like ratelimit.SpecsFromBindings.
func SpecsFromBindings(bindings []*apitypes.SubjectBinding) (bySubject map[SubjectKey]Spec, byIssuer map[string]Spec) {
	bySubject = map[SubjectKey]Spec{}
	byIssuer = map[string]Spec{}
	for _, sb := range bindings {
		if sb == nil || sb.Spec.Budget == nil {
			continue
		}
		spec := Spec{
			CPUSecondsPerDay: sb.Spec.Budget.CPUSecondsPerDay,
			RowsPerDay:       sb.Spec.Budget.RowsPerDay,
		}
		if spec.CPUSecondsPerDay == 0 && spec.RowsPerDay == 0 {
			continue
		}
		if sb.Spec.Claims.SubjectID != "" {
			bySubject[SubjectKey{Subject: sb.Spec.Claims.SubjectID, Issuer: identity.BindingIssuer(sb)}] = spec
		}
		if sb.Spec.Issuer != "" {
			byIssuer[sb.Spec.Issuer] = spec
		}
	}
	return bySubject, byIssuer
}
