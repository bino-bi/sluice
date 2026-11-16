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

// A star reads columns the query does not name, so every columnsAccessed
// trigger fires. A predicate trigger does not.
func TestApproval_StarFiresColumnTrigger(t *testing.T) {
	star := parser.QueryShape{AccessesAllColumns: true}
	cols := &apitypes.ApprovalWhen{ColumnsAccessed: []string{"ssn"}}
	if dec := evalApproval(t, star, allowAll(0), approvalPolicy("pii", cols)); dec.Approval == nil {
		t.Error("a star should trigger columnsAccessed")
	}
	preds := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "country", Op: "=", Value: "de"}}}
	if dec := evalApproval(t, star, allowAll(0), approvalPolicy("geo", preds)); dec.Approval != nil {
		t.Error("a star triggered a predicate trigger")
	}
}

// A column in a condition the parser cannot read as (column, op, value)
// fires the trigger on that column, whatever its op and value.
func TestApproval_UnreadPredicateFiresTrigger(t *testing.T) {
	when := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "country", Op: "=", Value: "de"}}}
	dec := evalApproval(t, parser.QueryShape{UnreadPredicateColumns: []string{"p.country"}}, allowAll(0), approvalPolicy("geo", when))
	if dec.Approval == nil {
		t.Error("an unread condition on country should trigger approval")
	}
	dec = evalApproval(t, parser.QueryShape{UnreadPredicateColumns: []string{"name"}}, allowAll(0), approvalPolicy("geo", when))
	if dec.Approval != nil {
		t.Error("approval triggered by an unread condition on another column")
	}
}

// IN compares with each list value, so it matches an "=" trigger.
func TestApproval_InMatchesEqualsTrigger(t *testing.T) {
	when := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "country", Op: "=", Value: "de"}}}
	in := func(v string) parser.QueryShape {
		return parser.QueryShape{Comparisons: []parser.Comparison{{Column: "country", Op: "in", Value: v}}}
	}
	if dec := evalApproval(t, in("de"), allowAll(0), approvalPolicy("geo", when)); dec.Approval == nil {
		t.Error("country IN ('de') should trigger approval")
	}
	if dec := evalApproval(t, in("fr"), allowAll(0), approvalPolicy("geo", when)); dec.Approval != nil {
		t.Error("country IN ('fr') triggered approval")
	}
}

// A trigger pattern matches without regard to case, as DuckDB resolves
// column names. The parser reports names in lower case.
func TestApproval_TriggerPatternsFoldCase(t *testing.T) {
	cols := &apitypes.ApprovalWhen{ColumnsAccessed: []string{"SSN"}}
	if dec := evalApproval(t, parser.QueryShape{AccessedColumns: []string{"ssn"}}, allowAll(0), approvalPolicy("pii", cols)); dec.Approval == nil {
		t.Error("pattern SSN should match the column ssn")
	}
	preds := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "Country", Op: "=", Value: "de"}}}
	shape := parser.QueryShape{Comparisons: []parser.Comparison{{Column: "country", Op: "=", Value: "de"}}}
	if dec := evalApproval(t, shape, allowAll(0), approvalPolicy("geo", preds)); dec.Approval == nil {
		t.Error("pattern Country should match the column country")
	}
}

// A pattern matches each part of a dotted name: a qualifier before the
// column, and a field after a STRUCT column.
func TestApproval_PatternMatchesEachNamePart(t *testing.T) {
	when := &apitypes.ApprovalWhen{ColumnsAccessed: []string{"profile"}}
	for _, col := range []string{"profile", "p.profile", "profile.ssn", "p.profile.ssn", "main.people.profile.ssn"} {
		if dec := evalApproval(t, parser.QueryShape{AccessedColumns: []string{col}}, allowAll(0), approvalPolicy("pii", when)); dec.Approval == nil {
			t.Errorf("pattern profile should match %s", col)
		}
	}
	if dec := evalApproval(t, parser.QueryShape{AccessedColumns: []string{"p.profiles", "id"}}, allowAll(0), approvalPolicy("pii", when)); dec.Approval != nil {
		t.Error("pattern profile matched another column")
	}
}

// A number written in another way is the same value.
func TestApproval_NumericValueMatches(t *testing.T) {
	when := &apitypes.ApprovalWhen{Predicates: []apitypes.PredicateTrigger{{Column: "tenant_id", Op: "=", Value: "7"}}}
	cmp := func(v string) parser.QueryShape {
		return parser.QueryShape{Comparisons: []parser.Comparison{{Column: "tenant_id", Op: "=", Value: v}}}
	}
	for _, v := range []string{"7", "7.0", "07", "7e0", " 7 "} {
		if dec := evalApproval(t, cmp(v), allowAll(0), approvalPolicy("tenant", when)); dec.Approval == nil {
			t.Errorf("tenant_id = %s should trigger approval", v)
		}
	}
	for _, v := range []string{"8", "70", "seven"} {
		if dec := evalApproval(t, cmp(v), allowAll(0), approvalPolicy("tenant", when)); dec.Approval != nil {
			t.Errorf("tenant_id = %s triggered approval", v)
		}
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

// An exclude on a gate kind carves out tables, not the whole statement:
// joining an excluded table must not switch the gate off for the others.
func TestGateExclude_JoinOfExcludedTableKeepsGate(t *testing.T) {
	pii := parser.TableRef{Catalog: "pg", Schema: "public", Table: "users"}
	lookup := parser.TableRef{Catalog: "pg", Schema: "public", Table: "public_lookup"}
	byTable := apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"public_lookup"}}}}}
	bySubject := apitypes.Selector{Any: []apitypes.Clause{{Subjects: &apitypes.SubjectSelector{Groups: []string{"stewards"}}}}}

	gate := approvalPolicy("gate", nil)
	gate.Spec.Exclude = &byTable
	reject := &apitypes.QueryRejectPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindQueryRejectPolicy},
		Metadata: apitypes.ObjectMeta{Name: "no-pii"},
		Spec: apitypes.QueryRejectSpec{
			Match:   apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			Exclude: &byTable,
			Reject:  apitypes.RejectSpec{Rules: []apitypes.RejectRule{{Name: "r", Message: "no"}}},
		},
	}
	eval := func(user *identity.UserCtx, pol apitypes.Object, tables ...parser.TableRef) *Decision {
		t.Helper()
		eng := New(Options{})
		if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), pol)); err != nil {
			t.Fatalf("apply: %v", err)
		}
		dec, err := eng.Evaluate(context.Background(), Input{User: user, AST: stubAST{kind: parser.StmtSelect}, Tables: tables})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		return dec
	}
	user := &identity.UserCtx{Subject: "u"}
	if dec := eval(user, gate, pii, lookup); dec.Approval == nil {
		t.Error("approval: JOIN of an excluded table switched the gate off")
	}
	if dec := eval(user, gate, lookup); dec.Approval != nil {
		t.Error("approval: a query on the excluded table only must not be gated")
	}
	if dec := eval(user, reject, pii, lookup); dec.Outcome != OutcomeReject {
		t.Errorf("reject: JOIN of an excluded table gave %s, want reject", dec.Outcome)
	}
	if dec := eval(user, reject, lookup); dec.Outcome != OutcomeAllow {
		t.Errorf("reject: excluded table only gave %s, want allow", dec.Outcome)
	}
	gate.Spec.Exclude = &bySubject
	if dec := eval(&identity.UserCtx{Subject: "s", Groups: []string{"stewards"}}, gate, pii, lookup); dec.Approval != nil {
		t.Error("approval: a subject exclude must switch the gate off")
	}

	// A table the gate never matched must not keep it on.
	hrPublic := parser.TableRef{Catalog: "pg", Schema: "public", Table: "hr_public"}
	orders := parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}
	payroll := parser.TableRef{Catalog: "pg", Schema: "public", Table: "payroll"}
	hr := approvalPolicy("hr", nil)
	hr.Spec.Match = apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"payroll", "hr_*"}}}}}
	hr.Spec.Exclude = &apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"hr_public"}}}}}
	if dec := eval(user, hr, hrPublic, orders); dec.Approval != nil {
		t.Error("approval: an unmatched joined table kept the gate on")
	}
	if dec := eval(user, hr, payroll, hrPublic); dec.Approval == nil {
		t.Error("approval: a matched table that is not excluded must keep the gate")
	}
}
