// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// SpecsFromBindings extracts per-subject and per-issuer rate-limit specs
// from every SubjectBinding.spec.rateLimit. A subject spec is keyed by
// claims.subjectId and the issuer the binding's subjects get; an issuer
// spec applies per authenticated subject under that issuer.
func SpecsFromBindings(bindings []*apitypes.SubjectBinding) (bySubject map[SubjectKey]Spec, byIssuer map[string]Spec) {
	bySubject = map[SubjectKey]Spec{}
	byIssuer = map[string]Spec{}
	for _, sb := range bindings {
		if sb == nil || sb.Spec.RateLimit == nil || sb.Spec.RateLimit.RPS <= 0 {
			continue
		}
		spec := Spec{RPS: sb.Spec.RateLimit.RPS, Burst: sb.Spec.RateLimit.Burst}
		if spec.Burst <= 0 {
			spec.Burst = 1
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
