// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit_test

import (
	"testing"

	"github.com/bino-bi/sluice/internal/budget"
	"github.com/bino-bi/sluice/internal/ratelimit"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// A subject spec is keyed by the issuer the binding's subjects get, so it
// does not reach the same sub of another issuer.
func TestSubjectSpecsAreKeyedByIssuer(t *testing.T) {
	binding := func(name, issuer string, keys bool) *apitypes.SubjectBinding {
		sb := &apitypes.SubjectBinding{
			Metadata: apitypes.ObjectMeta{Name: name},
			Spec: apitypes.SubjectBindingSpec{
				Issuer:    issuer,
				Claims:    apitypes.ClaimPaths{SubjectID: name + "-id"},
				RateLimit: &apitypes.RateLimitSpec{RPS: 1, Burst: 1},
				Budget:    &apitypes.BudgetSpec{RowsPerDay: 1},
			},
		}
		if keys {
			sb.Spec.APIKeys = []apitypes.APIKeyBinding{{ID: "k", HashRef: "secret://env/X"}}
		}
		return sb
	}
	bindings := []*apitypes.SubjectBinding{
		binding("bot", "", true),
		binding("svc", "https://idp", true),
		binding("any", "", false),
	}
	want := []ratelimit.SubjectKey{
		{Subject: "bot-id", Issuer: "bot"},
		{Subject: "svc-id", Issuer: "https://idp"},
		{Subject: "any-id"},
	}
	rate, _ := ratelimit.SpecsFromBindings(bindings)
	bud, _ := budget.SpecsFromBindings(bindings)
	if len(rate) != len(want) || len(bud) != len(want) {
		t.Fatalf("rate specs %v, budget specs %v, want the keys %v", rate, bud, want)
	}
	for _, k := range want {
		if _, ok := rate[k]; !ok {
			t.Errorf("rate spec %+v missing", k)
		}
		if _, ok := bud[budget.SubjectKey(k)]; !ok {
			t.Errorf("budget spec %+v missing", k)
		}
	}
}
