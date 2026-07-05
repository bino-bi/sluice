// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func maskPolicyWithExclude(name string, columns []string, match, exclude apitypes.Selector) *apitypes.ColumnMaskPolicy {
	spec := apitypes.ColumnMaskSpec{
		Match: match,
		Mask:  apitypes.MaskSpec{Type: apitypes.MaskNull},
	}
	if len(columns) > 0 {
		for i := range spec.Match.Any {
			if spec.Match.Any[i].Resources != nil {
				spec.Match.Any[i].Resources.Columns = columns
			}
		}
	}
	if len(exclude.Any) > 0 || len(exclude.All) > 0 {
		e := exclude
		spec.Exclude = &e
	}
	return &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec:     spec,
	}
}

func evalTables(t *testing.T, user *identity.UserCtx, objs []apitypes.Object, tables ...parser.TableRef) *Decision {
	t.Helper()
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(objs...)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{User: user, Tables: tables})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return dec
}

// TestExclude_ResourceCarveOutIsPerTable guards the multi-table exclude
// bypass: a resource-scoped exclude must only remove the named table, not
// drop the whole policy for the other tables in a multi-table query.
func TestExclude_ResourceCarveOutIsPerTable(t *testing.T) {
	mask := maskPolicyWithExclude("mask-ssn",
		[]string{"ssn"},
		apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
		apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"sandbox_stats"}}}}},
	)
	customers := parser.TableRef{Catalog: "pg", Schema: "public", Table: "customers"}
	sandbox := parser.TableRef{Catalog: "pg", Schema: "public", Table: "sandbox_stats"}

	dec := evalTables(t, &identity.UserCtx{Subject: "u"}, []apitypes.Object{allowAll(0), mask}, customers, sandbox)

	if _, ok := dec.ColumnMasks["pg.public.customers.ssn"]; !ok {
		t.Errorf("customers.ssn must stay masked when sandbox_stats is joined; masks=%v", keys(dec.ColumnMasks))
	}
	if _, ok := dec.ColumnMasks["pg.public.sandbox_stats.ssn"]; ok {
		t.Errorf("sandbox_stats.ssn must be excluded; masks=%v", keys(dec.ColumnMasks))
	}
}

// TestExclude_SubjectBreakGlassStillWorks guards that a subject-scoped
// exclude (break-glass) still lifts the mask for the excluded group and
// keeps it for everyone else — the exclude is evaluated with the user.
func TestExclude_SubjectBreakGlassStillWorks(t *testing.T) {
	mask := maskPolicyWithExclude("mask-email",
		[]string{"email"},
		apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
		apitypes.Selector{Any: []apitypes.Clause{{Subjects: &apitypes.SubjectSelector{Groups: []string{"sre"}}}}},
	)
	customers := parser.TableRef{Catalog: "pg", Schema: "public", Table: "customers"}

	sre := &identity.UserCtx{Subject: "on-call", Groups: []string{"analytics", "sre"}}
	decSRE := evalTables(t, sre, []apitypes.Object{allowAll(0), mask}, customers)
	if _, ok := decSRE.ColumnMasks["pg.public.customers.email"]; ok {
		t.Errorf("break-glass: sre must see unmasked email; masks=%v", keys(decSRE.ColumnMasks))
	}

	analyst := &identity.UserCtx{Subject: "analyst", Groups: []string{"analytics"}}
	decAnalyst := evalTables(t, analyst, []apitypes.Object{allowAll(0), mask}, customers)
	if _, ok := decAnalyst.ColumnMasks["pg.public.customers.email"]; !ok {
		t.Errorf("non-sre must have email masked; masks=%v", keys(decAnalyst.ColumnMasks))
	}
}

type stubAST struct{ kind parser.StmtKind }

func (a stubAST) Raw() any                   { return nil }
func (a stubAST) Fingerprint() string        { return "" }
func (a stubAST) Tables() []parser.TableRef  { return nil }
func (a stubAST) Catalogs() []string         { return nil }
func (a stubAST) Shape() parser.QueryShape   { return parser.QueryShape{} }
func (a stubAST) Clone() parser.AST          { return a }
func (a stubAST) Statement() parser.StmtKind { return a.kind }
func (a stubAST) Source() string             { return "" }

// TestActionScoping guards that ResourceSelector.Actions is honoured: a
// deny scoped to DELETE must not fire on a SELECT (before the fix an
// actions-only selector matched every statement and every table).
func TestActionScoping(t *testing.T) {
	denyDelete := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "deny-delete", Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectDeny,
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Actions: []apitypes.Action{apitypes.ActionDelete}}},
			}},
		},
	}
	orders := parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}

	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), denyDelete)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A SELECT must not be caught by the DELETE-scoped deny.
	dec, _ := eng.Evaluate(context.Background(), Input{
		Tables: []parser.TableRef{orders},
		AST:    stubAST{kind: parser.StmtSelect},
	})
	if dec.Outcome != OutcomeAllow {
		t.Errorf("SELECT outcome = %s, want allow (DELETE-scoped deny must not fire)", dec.Outcome)
	}

	// A DELETE (verb known) must be caught by the deny.
	decDel, _ := eng.Evaluate(context.Background(), Input{
		Tables: []parser.TableRef{orders},
		AST:    stubAST{kind: parser.StmtDelete},
	})
	if decDel.Outcome != OutcomeDeny {
		t.Errorf("DELETE outcome = %s, want deny", decDel.Outcome)
	}
}

// TestEnforcementModeShadow guards Audit/DryRun modes: a policy in a
// non-Enforce mode must match (and be recorded as shadow) without affecting
// the effective decision. This also confirms such a policy no longer bricks
// snapshot compilation.
func TestEnforcementModeShadow(t *testing.T) {
	auditDeny := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "audit-deny", Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			EnforcementMode: apitypes.EnforcementAudit,
			Effect:          apitypes.EffectDeny,
			Match:           apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
		},
	}
	orders := parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}

	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), auditDeny)); err != nil {
		t.Fatalf("apply (audit-mode policy must not brick reload): %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		Tables: []parser.TableRef{orders},
		AST:    stubAST{kind: parser.StmtSelect},
	})
	if dec.Outcome != OutcomeAllow {
		t.Errorf("audit-mode deny must not enforce; outcome = %s", dec.Outcome)
	}
	if len(dec.Shadow) != 1 || dec.Shadow[0].Name != "audit-deny" {
		t.Errorf("audit-mode deny must be recorded as shadow, got %+v", dec.Shadow)
	}
}

// TestDeepEqualNumericStrictTyping guards the claim type-confusion fix: a
// numeric claim must never equal a string policy value, while int/float
// shape differences from JSON vs YAML still compare equal.
func TestDeepEqualNumericStrictTyping(t *testing.T) {
	if deepEqual(float64(1), "1") {
		t.Error("numeric 1 must not equal string \"1\"")
	}
	if deepEqual(true, "true") {
		t.Error("bool true must not equal string \"true\"")
	}
	if deepEqual(nil, "<nil>") {
		t.Error("nil must not equal the string \"<nil>\"")
	}
	if !deepEqual(1, 1.0) {
		t.Error("int 1 should equal float64 1.0 (JSON/YAML shape difference)")
	}
	if !deepEqual("acme", "acme") {
		t.Error("equal strings must match")
	}
	if deepEqual(1, 2) {
		t.Error("1 must not equal 2")
	}
}

func keys(m map[string]*CompiledMask) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
