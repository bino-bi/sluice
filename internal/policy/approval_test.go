// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func approvalPolicy(name string, when *apitypes.ApprovalWhen) *apitypes.ApprovalPolicy {
	return &apitypes.ApprovalPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindApprovalPolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: 100},
		Spec: apitypes.ApprovalSpec{
			Match:  apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			When:   when,
			Reason: "needs steward sign-off",
		},
	}
}

func evalApproval(t *testing.T, shape parser.QueryShape, pols ...apitypes.Object) *Decision {
	t.Helper()
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(pols...)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		AST:    stubAST{kind: parser.StmtSelect},
		Shape:  shape,
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "users"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return dec
}

func TestApproval_EmptyWhenTriggersOnSelector(t *testing.T) {
	dec := evalApproval(t, parser.QueryShape{}, allowAll(0), approvalPolicy("gate", nil))
	if dec.Approval == nil {
		t.Fatal("empty When should trigger approval on selector match")
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("outcome = %s, want allow (approval gates an allow)", dec.Outcome)
	}
}

func TestApproval_ColumnTrigger(t *testing.T) {
	when := &apitypes.ApprovalWhen{ColumnsAccessed: []string{"ssn", "salary*"}}
	// Column present -> trigger.
	dec := evalApproval(t, parser.QueryShape{AccessedColumns: []string{"id", "ssn"}}, allowAll(0), approvalPolicy("pii", when))
	if dec.Approval == nil {
		t.Error("accessing ssn should trigger approval")
	}
	// Column absent -> no trigger.
	dec = evalApproval(t, parser.QueryShape{AccessedColumns: []string{"id", "name"}}, allowAll(0), approvalPolicy("pii", when))
	if dec.Approval != nil {
		t.Error("approval triggered without a matching column")
	}
}

func TestApproval_PredicateTrigger(t *testing.T) {
	when := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "country", Op: "=", Value: "de"}}}
	dec := evalApproval(t, parser.QueryShape{Comparisons: []parser.Comparison{{Column: "country", Op: "=", Value: "de"}}}, allowAll(0), approvalPolicy("geo", when))
	if dec.Approval == nil {
		t.Error("country = de should trigger approval")
	}
	// Different value -> no trigger.
	dec = evalApproval(t, parser.QueryShape{Comparisons: []parser.Comparison{{Column: "country", Op: "=", Value: "us"}}}, allowAll(0), approvalPolicy("geo", when))
	if dec.Approval != nil {
		t.Error("approval triggered on a non-matching value")
	}
}

func TestApproval_AggregatesMultiplePolicies(t *testing.T) {
	p1 := approvalPolicy("pii", &apitypes.ApprovalWhen{ColumnsAccessed: []string{"ssn"}})
	p2 := approvalPolicy("geo", &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "country"}}})
	dec := evalApproval(t, parser.QueryShape{
		AccessedColumns: []string{"ssn"},
		Comparisons:     []parser.Comparison{{Column: "country", Op: "=", Value: "de"}},
	}, allowAll(0), p1, p2)
	if dec.Approval == nil || len(dec.Approval.Policies) != 2 {
		t.Fatalf("expected both approval policies aggregated, got %+v", dec.Approval)
	}
	if len(dec.Approval.Reasons) != 2 {
		t.Errorf("expected 2 reasons, got %v", dec.Approval.Reasons)
	}
}

func TestApproval_DenyBeatsApproval(t *testing.T) {
	dec := evalApproval(t, parser.QueryShape{}, denyPIIAll(), approvalPolicy("gate", nil))
	if dec.Outcome != OutcomeDeny {
		t.Errorf("outcome = %s, want deny (deny > approval)", dec.Outcome)
	}
	if dec.Approval != nil {
		t.Error("approval set on a denied request")
	}
}

func TestApproval_ShadowMode(t *testing.T) {
	p := approvalPolicy("gate", nil)
	p.Spec.EnforcementMode = apitypes.EnforcementAudit
	dec := evalApproval(t, parser.QueryShape{}, allowAll(0), p)
	if dec.Approval != nil {
		t.Error("Audit-mode approval must not gate the request")
	}
	if len(dec.ApprovalShadow) != 1 {
		t.Errorf("expected shadow approval recorded, got %+v", dec.ApprovalShadow)
	}
}

// denyPIIAll denies everything so deny-vs-approval precedence can be tested.
func denyPIIAll() *apitypes.SQLAccessPolicy {
	return &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "deny-all", Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectDeny,
			Match:  apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
		},
	}
}
