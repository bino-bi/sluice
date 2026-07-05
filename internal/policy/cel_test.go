// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func rowFilterExpr(name, expr string) *apitypes.RowFilterPolicy {
	return &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: 50},
		Spec: apitypes.RowFilterSpec{
			Match:  apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			Filter: apitypes.FilterSpec{Expression: expr},
		},
	}
}

func TestCELConditionGatesPolicy(t *testing.T) {
	mask := &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "mask-eu", Priority: 50},
		Spec: apitypes.ColumnMaskSpec{
			Match:      apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}, Columns: []string{"email"}}}}},
			Conditions: []apitypes.Condition{{Name: "eu-only", Expression: `request.headers["x-region"] == "eu"`}},
			Mask:       apitypes.MaskSpec{Type: apitypes.MaskNull},
		},
	}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), mask)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}}

	// Condition true → mask applies.
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:    &identity.UserCtx{Subject: "u"},
		Tables:  tables,
		Request: &RequestFacts{Headers: map[string]string{"x-region": "eu"}},
	})
	if len(dec.ColumnMasks) == 0 {
		t.Error("mask not applied when condition true")
	}

	// Condition false → mask skipped.
	dec, _ = eng.Evaluate(context.Background(), Input{
		User:    &identity.UserCtx{Subject: "u"},
		Tables:  tables,
		Request: &RequestFacts{Headers: map[string]string{"x-region": "us"}},
	})
	if len(dec.ColumnMasks) != 0 {
		t.Error("mask applied when condition false")
	}
}

func TestCELConditionEvalErrorDenies(t *testing.T) {
	// Referencing a claim that is absent as a map index errors at runtime.
	access := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "cond-allow"},
		Spec: apitypes.SQLAccessSpec{
			Effect:     apitypes.EffectAllow,
			Match:      apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			Conditions: []apitypes.Condition{{Name: "needs-claim", Expression: `subject.claims["tier"] == "gold"`}},
		},
	}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(access)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u", Claims: map[string]any{}},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("outcome = %s, want deny on condition eval error", dec.Outcome)
	}
}

func TestCELRowFilterToPredicate(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string // rendered leaf description
	}{
		{"equals-literal", `row.tenant_id == "acme"`, "tenant_id Equals"},
		{"template", `row.tenant_id == subject.claims.tenant_id`, "tenant_id Equals[tmpl]"},
		{"in-list", `row.region in ["eu", "eea"]`, "region In[2]"},
		{"and", `row.a == 1 && row.b == 2`, "All"},
		{"or", `row.a == 1 || row.b == 2`, "Any"},
		{"reversed", `5 < row.age`, "age GreaterThan"},
		{"startswith", `row.name.startsWith("A")`, "name Like"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pred, err := compileFilterExpression(tc.expr)
			if err != nil {
				t.Fatalf("compile %q: %v", tc.expr, err)
			}
			got := describePred(pred)
			if !strings.Contains(got, strings.Split(tc.want, "[")[0]) {
				t.Errorf("expr %q lowered to %q, want ~%q", tc.expr, got, tc.want)
			}
		})
	}
}

func describePred(p *CompiledPredicate) string {
	if p == nil {
		return "<nil>"
	}
	if len(p.All) > 0 {
		return "All"
	}
	if len(p.Any) > 0 {
		return "Any"
	}
	if p.Not != nil {
		return "Not"
	}
	s := p.Column + " " + string(p.Op)
	for _, v := range p.Values {
		if v.Template != nil {
			s += "[tmpl]"
		}
	}
	return s
}

func TestCELRowFilterRejectsOutOfSubset(t *testing.T) {
	for _, expr := range []string{
		`row.a + 1 == 2`,        // arithmetic
		`size(row.tags) > 0`,    // function call
		`query.has_select_star`, // query.* in a row filter
		`row.a == row.b`,        // two columns
	} {
		if _, err := compileFilterExpression(expr); err == nil {
			t.Errorf("expr %q accepted, want rejection", expr)
		}
	}
}

func TestCELRowFilterEndToEnd(t *testing.T) {
	eng := New(Options{})
	pol := rowFilterExpr("tenant", `row.tenant_id == subject.claims.tenant_id && row.region in ["eu"]`)
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), pol)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "acme"}},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	f, ok := dec.RowFilters["pg.public.orders"]
	if !ok {
		t.Fatalf("no row filter produced: %+v", dec.RowFilters)
	}
	if !f.Predicate.HasTemplate() {
		t.Error("filter predicate lost its subject template")
	}
}

func TestCELRejectExpression(t *testing.T) {
	reject := &apitypes.QueryRejectPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindQueryRejectPolicy},
		Metadata: apitypes.ObjectMeta{Name: "no-wide-star", Priority: 50},
		Spec: apitypes.QueryRejectSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			Reject: apitypes.RejectSpec{Rules: []apitypes.RejectRule{
				{Name: "star-join", Expression: `query.has_select_star && query.joins > 0`, Message: "no SELECT * across joins"},
			}},
		},
	}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), reject)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}}

	// Rule fires.
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: tables,
		Shape:  parser.QueryShape{HasSelectStar: true, Joins: 1},
	})
	if dec.Outcome != OutcomeReject {
		t.Errorf("outcome = %s, want reject when rule fires", dec.Outcome)
	}

	// Rule does not fire (no star).
	dec, _ = eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: tables,
		Shape:  parser.QueryShape{HasSelectStar: false, Joins: 1},
	})
	if dec.Outcome == OutcomeReject {
		t.Errorf("outcome = reject when rule should not fire")
	}
}

func TestCELInvalidExpressionFailsCompile(t *testing.T) {
	pol := rowFilterExpr("bad", `this is not cel`)
	_, err := Compile(context.Background(), makeSnapshot(pol))
	if err == nil {
		t.Fatal("invalid CEL expression compiled successfully")
	}
}
