// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	"github.com/bino-bi/sluice/pkg/mask"
)

// makeEngine sets up a policy engine with the supplied policies; returns
// an Evaluate closure that runs the given SQL through parser → policy
// engine and hands the decision back.
func buildRewriter(t *testing.T) *rewriter.Rewriter {
	t.Helper()
	p := pgquery.New(parser.Options{})
	return rewriter.New(rewriter.Options{
		Parser:         p,
		DefaultCatalog: "pg",
	})
}

func evaluate(t *testing.T, sql string, user *identity.UserCtx, policies ...apitypes.Object) (*policy.Decision, parser.AST) {
	t.Helper()
	p := pgquery.New(parser.Options{})
	ast, err := p.Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	snap := &struct{}{}
	_ = snap

	eng := policy.New(policy.Options{})
	src := &config.Snapshot{
		Policies: append([]apitypes.Object(nil), policies...),
		ByKind:   map[apitypes.Kind][]apitypes.Object{},
	}
	for _, o := range policies {
		src.ByKind[o.GetKind()] = append(src.ByKind[o.GetKind()], o)
	}
	if err := eng.ApplySnapshot(context.Background(), src); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), policy.Input{
		User:   user,
		AST:    ast,
		Shape:  ast.Shape(),
		Tables: ast.Tables(),
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return dec, ast
}

func TestRewriter_PassThrough(t *testing.T) {
	rw := buildRewriter(t)
	dec, ast := evaluate(t, "SELECT id FROM pg.public.orders", nil, allowAll(0))

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST:      ast,
		Decision: dec,
		Raw:      "SELECT id FROM pg.public.orders",
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.Changed {
		t.Errorf("changed = true, want false for pass-through")
	}
	if res.SQL != "SELECT id FROM pg.public.orders" {
		t.Errorf("SQL = %q", res.SQL)
	}
}

func TestRewriter_Deny(t *testing.T) {
	rw := buildRewriter(t)
	dec, ast := evaluate(t, "SELECT * FROM pg.public.customer_pii", nil,
		allowAll(0), denyPII())

	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST:      ast,
		Decision: dec,
		Raw:      "SELECT * FROM pg.public.customer_pii",
	})
	if err == nil {
		t.Fatal("expected deny error, got nil")
	}
	if !strings.Contains(err.Error(), "ACL_DENIED") {
		t.Errorf("want ACL_DENIED, got %v", err)
	}
}

func TestRewriter_RowFilterTemplate(t *testing.T) {
	rw := buildRewriter(t)
	user := &identity.UserCtx{
		Subject: "u",
		Claims:  map[string]any{"tenant_id": "t-1"},
	}
	rf := &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: "tenant-filter"},
		Spec: apitypes.RowFilterSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}}},
			}},
			Filter: apitypes.FilterSpec{
				Predicate: &apitypes.Predicate{
					Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "{{ subject.tenant_id }}",
				},
			},
		},
	}
	sql := "SELECT id FROM pg.public.orders"
	dec, ast := evaluate(t, sql, user, allowAll(0), rf)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST:      ast,
		Decision: dec,
		User:     user,
		Raw:      sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.Changed {
		t.Error("changed = false, want true")
	}
	if len(res.Params) != 1 || res.Params[0] != "t-1" {
		t.Errorf("params = %v, want [t-1]", res.Params)
	}
	if !strings.Contains(res.SQL, "tenant_id = $1") {
		t.Errorf("SQL should contain 'tenant_id = $1': %q", res.SQL)
	}
	// The RangeSubselect wrapper must preserve the table name.
	if !strings.Contains(res.SQL, "pg.public.orders") {
		t.Errorf("table name missing in rewritten SQL: %q", res.SQL)
	}
}

func TestRewriter_ColumnMaskConstant(t *testing.T) {
	rw := buildRewriter(t)
	m := &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "mask-email"},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}, Columns: []string{"email"}}},
			}},
			Mask: apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: "***"}},
		},
	}
	sql := "SELECT id, email FROM pg.public.customers"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST:      ast,
		Decision: dec,
		Raw:      sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.Changed {
		t.Error("changed = false, want true")
	}
	if !strings.Contains(res.SQL, "'***'") {
		t.Errorf("constant mask missing from SQL: %q", res.SQL)
	}
	if !strings.Contains(res.SQL, "email") {
		// Column alias must be preserved so the client sees `email`.
		t.Errorf("column alias missing: %q", res.SQL)
	}
}

func TestRewriter_ColumnMaskNull(t *testing.T) {
	rw := buildRewriter(t)
	m := &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "mask-ssn"},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"employees"}, Columns: []string{"ssn"}}},
			}},
			Mask: apitypes.MaskSpec{Type: apitypes.MaskNull},
		},
	}
	sql := "SELECT id, ssn FROM pg.hr.employees"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "NULL AS ssn") {
		t.Errorf("expected 'NULL AS ssn': %q", res.SQL)
	}
}

func TestRewriter_RejectStatement(t *testing.T) {
	rw := buildRewriter(t)
	dec, ast := evaluate(t, "UPDATE pg.public.orders SET total = 0", &identity.UserCtx{Subject: "u"}, allowAll(0))
	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: "UPDATE pg.public.orders SET total = 0",
	})
	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "ACL_REJECTED") {
		t.Errorf("want ACL_REJECTED, got %v", err)
	}
}

// allowAll / denyPII re-declared here because policy's tests define them
// in package policy — not visible from this _test package.
func allowAll(prio int32) *apitypes.SQLAccessPolicy {
	return &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "allow-all", Priority: prio},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectAllow,
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}},
			}},
		},
	}
}

func denyPII() *apitypes.SQLAccessPolicy {
	return &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "deny-pii", Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectDeny, Message: "no PII", ErrorCode: "ACL_DENIED",
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"*_pii"}}},
			}},
		},
	}
}

func TestRewriter_SmokeMask(t *testing.T) {
	// Confirm that mask.Default still ships with null + constant providers
	// so the rewriter unit tests do not silently flag a wrong provider.
	reg := mask.Default()
	for _, typ := range []string{"null", "constant"} {
		if _, ok := reg.Lookup(typ); !ok {
			t.Errorf("mask provider %q missing from Default registry", typ)
		}
	}
}
