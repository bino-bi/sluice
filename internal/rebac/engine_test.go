// SPDX-License-Identifier: AGPL-3.0-or-later

package rebac_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rebac"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func relPolicy() *apitypes.RelationshipPolicy {
	return &apitypes.RelationshipPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionBeta1, Kind: apitypes.KindRelationshipPolicy},
		Metadata: apitypes.ObjectMeta{Name: "fga-finance"},
		Spec: apitypes.RelationshipSpec{
			Match:   apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Schemas: []string{"finance"}}}}},
			Backend: apitypes.RelationshipBackend{Type: "openfga", Endpoint: "http://fga", StoreID: "s1"},
			Checks:  []apitypes.RelationCheck{{ObjectTemplate: "table:{{schema}}.{{table}}", Relation: "viewer"}},
		},
	}
}

func newEngine(t *testing.T, fake *rebac.Fake) *rebac.Engine {
	t.Helper()
	e := rebac.New(rebac.Options{
		Clock:      func() time.Time { return time.Unix(1_700_000_000, 0) },
		NewChecker: func(apitypes.RelationshipBackend, []byte) rebac.RelationChecker { return fake },
	})
	snap := &config.Snapshot{RelationshipPolicies: []*apitypes.RelationshipPolicy{relPolicy()}}
	if err := e.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	return e
}

var financeTable = parser.TableRef{Catalog: "pg", Schema: "finance", Table: "ledger"}

func input() policy.Input {
	return policy.Input{User: &identity.UserCtx{Subject: "alice"}, Tables: []parser.TableRef{financeTable}}
}

// Passing checks restrict, they do not grant: the engine records the
// policy and abstains, so access still needs an allow from another member.
func TestReBAC_PassingChecksDoNotGrant(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{"table:finance.ledger#viewer@user:alice": true}}
	dec, err := newEngine(t, fake).Evaluate(context.Background(), input())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != policy.OutcomeDeny || !dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want abstain", dec.Outcome, dec.Abstained)
	}
	if len(dec.Applied) != 1 || dec.Applied[0].Name != "fga-finance" {
		t.Errorf("applied = %+v, want the relationship policy recorded", dec.Applied)
	}
}

// With [yaml, rebac] a query needs an SqlAccessPolicy allow for every table
// and every matching relationship check must pass.
func TestReBAC_CompositeNeedsTheYAMLAllow(t *testing.T) {
	allowLedger := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "allow-ledger", Priority: 100},
		Spec: apitypes.SQLAccessSpec{Effect: apitypes.EffectAllow, Match: apitypes.Selector{Any: []apitypes.Clause{
			{Resources: &apitypes.ResourceSelector{Catalogs: []string{"pg"}, Schemas: []string{"finance"}, Tables: []string{"ledger"}}},
		}}},
	}
	salaries := parser.TableRef{Catalog: "pg", Schema: "hr", Table: "salaries"}
	related := &rebac.Fake{Tuples: map[string]bool{"table:finance.ledger#viewer@user:alice": true}}
	cases := []struct {
		name   string
		yaml   []apitypes.Object
		fake   *rebac.Fake
		tables []parser.TableRef
		want   policy.Outcome
	}{
		{"check passes, no allow", nil, related, []parser.TableRef{financeTable}, policy.OutcomeDeny},
		{"check passes, joined table has no allow", []apitypes.Object{allowLedger}, related, []parser.TableRef{financeTable, salaries}, policy.OutcomeDeny},
		{"check passes, table allowed", []apitypes.Object{allowLedger}, related, []parser.TableRef{financeTable}, policy.OutcomeAllow},
		{"check fails, table allowed", []apitypes.Object{allowLedger}, &rebac.Fake{Tuples: map[string]bool{}}, []parser.TableRef{financeTable}, policy.OutcomeDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yaml := policy.New(policy.Options{})
			snap := &config.Snapshot{Policies: tc.yaml, ByKind: map[apitypes.Kind][]apitypes.Object{}}
			for _, o := range tc.yaml {
				snap.ByKind[o.GetKind()] = append(snap.ByKind[o.GetKind()], o)
			}
			if err := yaml.ApplySnapshot(context.Background(), snap); err != nil {
				t.Fatalf("apply yaml: %v", err)
			}
			in := policy.Input{User: &identity.UserCtx{Subject: "alice"}, Tables: tc.tables}
			dec, err := policy.NewComposite(policy.Options{}, yaml, newEngine(t, tc.fake)).Evaluate(context.Background(), in)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if dec.Outcome != tc.want {
				t.Errorf("outcome = %s (%+v), want %s", dec.Outcome, dec.DenyReason, tc.want)
			}
		})
	}
}

func TestReBAC_DenyWhenNotRelated(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{}}
	dec, _ := newEngine(t, fake).Evaluate(context.Background(), input())
	if dec.Outcome != policy.OutcomeDeny || dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want explicit deny", dec.Outcome, dec.Abstained)
	}
}

func TestReBAC_BackendErrorFailsClosed(t *testing.T) {
	fake := &rebac.Fake{Err: errors.New("fga down")}
	if _, err := newEngine(t, fake).Evaluate(context.Background(), input()); err == nil {
		t.Fatal("backend error must propagate (fail-closed)")
	}
}

func TestReBAC_AbstainWhenNoPolicyMatches(t *testing.T) {
	fake := &rebac.Fake{}
	e := newEngine(t, fake)
	// A table outside the finance schema matches no policy → abstain.
	dec, _ := e.Evaluate(context.Background(), policy.Input{
		User:   &identity.UserCtx{Subject: "alice"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if dec.Outcome != policy.OutcomeDeny || !dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want abstain", dec.Outcome, dec.Abstained)
	}
}

func TestReBAC_CachesChecks(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{"table:finance.ledger#viewer@user:alice": true}}
	e := newEngine(t, fake)
	_, _ = e.Evaluate(context.Background(), input())
	_, _ = e.Evaluate(context.Background(), input())
	if fake.Calls != 1 {
		t.Errorf("checker called %d times, want 1 (cached)", fake.Calls)
	}
}

// A RelationshipPolicy scoped to an action must run for that action. The
// engine did not pass the action to the selector, so such a policy never
// matched and its failing check was skipped.
func TestReBAC_ActionScopedPolicyRuns(t *testing.T) {
	scoped := relPolicy()
	scoped.Spec.Match.Any[0].Resources.Actions = []apitypes.Action{apitypes.ActionSelect}
	e := rebac.New(rebac.Options{
		Clock: func() time.Time { return time.Unix(1_700_000_000, 0) },
		NewChecker: func(apitypes.RelationshipBackend, []byte) rebac.RelationChecker {
			return &rebac.Fake{Tuples: map[string]bool{}}
		},
	})
	if err := e.ApplySnapshot(context.Background(), &config.Snapshot{RelationshipPolicies: []*apitypes.RelationshipPolicy{scoped}}); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	p := pgquery.New(parser.Options{})
	ast, err := p.Parse(context.Background(), "SELECT * FROM pg.finance.ledger")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec, err := e.Evaluate(context.Background(), policy.Input{User: &identity.UserCtx{Subject: "alice"}, AST: ast, Tables: ast.Tables()})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != policy.OutcomeDeny || dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want the failing check to deny", dec.Outcome, dec.Abstained)
	}
}

// In Audit mode a failed check does not deny. The decision lists the
// policy with would=deny, which the audit record shows.
func TestReBAC_AuditModeFailedCheckIsShadowDeny(t *testing.T) {
	rp := relPolicy()
	rp.Spec.EnforcementMode = apitypes.EnforcementAudit
	e := rebac.New(rebac.Options{
		NewChecker: func(apitypes.RelationshipBackend, []byte) rebac.RelationChecker {
			return &rebac.Fake{Tuples: map[string]bool{}}
		},
	})
	if err := e.ApplySnapshot(context.Background(), &config.Snapshot{RelationshipPolicies: []*apitypes.RelationshipPolicy{rp}}); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	dec, err := e.Evaluate(context.Background(), input())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec.Outcome == policy.OutcomeDeny && !dec.Abstained {
		t.Fatalf("an Audit policy denied: %+v", dec.DenyReason)
	}
	if len(dec.Shadow) != 1 || dec.Shadow[0].Name != "fga-finance" || dec.Shadow[0].Would != "deny" {
		t.Errorf("shadow = %+v, want fga-finance with would=deny", dec.Shadow)
	}
}
