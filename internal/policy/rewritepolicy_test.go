// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func rewritePolicy(name string, priority int32, spec apitypes.RewriteSpec) *apitypes.QueryRewritePolicy {
	return &apitypes.QueryRewritePolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindQueryRewritePolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: priority},
		Spec: apitypes.QueryRewriteSpec{
			Match: apitypes.Selector{
				Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}},
			},
			Rewrite: spec,
		},
	}
}

func TestCompileRewrite_Validation(t *testing.T) {
	cases := []struct {
		name    string
		spec    apitypes.RewriteSpec
		wantErr string
	}{
		{"limit ok", apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: 100}}, ""},
		{"limit zero", apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: 0}}, "limit.max"},
		{"limit negative", apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: -5}}, "limit.max"},
		{"limit overflow", apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: 1 << 40}}, "limit.max"},
		{"sample ok default method", apitypes.RewriteSpec{Sample: &apitypes.SampleSpec{Rate: 0.5}}, ""},
		{"sample rate zero", apitypes.RewriteSpec{Sample: &apitypes.SampleSpec{Rate: 0}}, "sample.rate"},
		{"sample rate over one", apitypes.RewriteSpec{Sample: &apitypes.SampleSpec{Rate: 1.5}}, "sample.rate"},
		{"sample bad method", apitypes.RewriteSpec{Sample: &apitypes.SampleSpec{Rate: 0.5, Method: "coinflip"}}, "sample.method"},
		{"timeout ok", apitypes.RewriteSpec{Timeout: apitypes.Duration(10 * time.Second)}, ""},
		{"timeout negative", apitypes.RewriteSpec{Timeout: apitypes.Duration(-time.Second)}, "timeout"},
		{"hints rejected", apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: 10}, Hints: []apitypes.HintEntry{{Key: "k", Value: "v"}}}, "hint"},
		{"empty spec", apitypes.RewriteSpec{}, "at least one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(context.Background(), makeSnapshot(rewritePolicy("p", 0, tc.spec)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCompileRewrite_SampleDefaultsToReservoir(t *testing.T) {
	snap, err := Compile(context.Background(), makeSnapshot(
		rewritePolicy("p", 0, apitypes.RewriteSpec{Sample: &apitypes.SampleSpec{Rate: 0.2}}),
	))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := snap.Policies[0].QueryRewrite.Sample
	if got == nil || got.Method != apitypes.SampleReservoir {
		t.Fatalf("sample method = %+v, want reservoir", got)
	}
}

func TestCollectRewrites_RestrictiveFold(t *testing.T) {
	eng := New(Options{})
	err := eng.ApplySnapshot(context.Background(), makeSnapshot(
		allowAll(0),
		rewritePolicy("loose", 90, apitypes.RewriteSpec{
			Limit:   &apitypes.LimitSpec{Max: 10_000},
			Sample:  &apitypes.SampleSpec{Rate: 0.5, Method: apitypes.SampleBernoulli},
			Timeout: apitypes.Duration(60 * time.Second),
		}),
		rewritePolicy("tight", 10, apitypes.RewriteSpec{
			Limit:   &apitypes.LimitSpec{Max: 100},
			Sample:  &apitypes.SampleSpec{Rate: 0.1},
			Timeout: apitypes.Duration(5 * time.Second),
		}),
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	eff := dec.Rewrite
	if eff == nil {
		t.Fatal("Decision.Rewrite is nil")
	}
	if eff.LimitMax != 100 {
		t.Errorf("LimitMax = %d, want 100 (min wins)", eff.LimitMax)
	}
	if eff.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s (min wins)", eff.Timeout)
	}
	// Sample comes from the highest-priority policy carrying one.
	if eff.Sample == nil || eff.Sample.Method != apitypes.SampleBernoulli || eff.Sample.Rate != 0.5 {
		t.Errorf("Sample = %+v, want bernoulli/0.5 from priority-90 policy", eff.Sample)
	}
	if len(eff.Policies) != 2 {
		t.Errorf("Policies = %v, want both names", eff.Policies)
	}
}

func TestCollectRewrites_ShadowModeDoesNotApply(t *testing.T) {
	p := rewritePolicy("shadow-cap", 50, apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: 10}})
	p.Spec.EnforcementMode = apitypes.EnforcementAudit
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), p)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Rewrite != nil {
		t.Errorf("Decision.Rewrite = %+v, want nil for Audit-mode policy", dec.Rewrite)
	}
	if len(dec.Shadow) != 1 || dec.Shadow[0].Name != "shadow-cap" {
		t.Errorf("Shadow = %+v, want the audit-mode policy recorded", dec.Shadow)
	}
}
