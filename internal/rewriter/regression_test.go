// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// maskPolicy builds a ColumnMaskPolicy matching table (by unqualified
// name) and the given column selector patterns.
func maskPolicy(name, table string, columns []string, spec apitypes.MaskSpec) *apitypes.ColumnMaskPolicy {
	return &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{table}, Columns: columns}},
			}},
			Mask: spec,
		},
	}
}

// TestRewriter_WildcardColumnMaskApplies guards the wildcard-column-mask
// no-op bug: a mask whose column selector is a wildcard pattern must still
// mask the concrete matching column. Before the fix the rewriter did an
// exact-key lookup, so `ssn_*` masked nothing (a PII leak).
func TestRewriter_WildcardColumnMaskApplies(t *testing.T) {
	rw := buildRewriter(t)
	m := maskPolicy("mask-ssn", "users", []string{"ssn_*"}, apitypes.MaskSpec{Type: apitypes.MaskNull})
	sql := "SELECT id, ssn_last4 FROM pg.public.users"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.Changed {
		t.Fatal("changed = false, want true (wildcard mask must apply)")
	}
	if !strings.Contains(res.SQL, "NULL AS ssn_last4") {
		t.Errorf("wildcard mask not applied to concrete column: %q", res.SQL)
	}
}

// TestRewriter_StarColumnMaskApplies guards the `columns: ["*"]` case —
// masking every column of a table.
func TestRewriter_StarColumnMaskApplies(t *testing.T) {
	rw := buildRewriter(t)
	m := maskPolicy("mask-all", "secrets", []string{"*"},
		apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: "***"}})
	sql := "SELECT token FROM pg.public.secrets"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "'***'") || !strings.Contains(res.SQL, "token") {
		t.Errorf("star mask not applied: %q", res.SQL)
	}
}

// TestRewriter_MaskSubstitutesInAllExpressionContexts guards the generic
// expression walk: a masked column must be substituted wherever it
// appears — sort keys, IN left-hand sides, function args — not only in
// bare target-list refs and comparison operands. Before the generic walk
// these contexts silently returned the raw column (a PII leak).
func TestRewriter_MaskSubstitutesInAllExpressionContexts(t *testing.T) {
	rw := buildRewriter(t)
	m := maskPolicy("mask-ssn", "emp", []string{"ssn"}, apitypes.MaskSpec{Type: apitypes.MaskNull})
	sql := "SELECT upper(ssn) AS u FROM pg.hr.emp WHERE ssn IN (SELECT ssn FROM pg.hr.emp) ORDER BY ssn"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(res.SQL, "ssn)") || strings.HasSuffix(res.SQL, "ssn") {
		t.Errorf("raw masked column survives in rewritten SQL: %q", res.SQL)
	}
	if !strings.Contains(res.SQL, "upper(NULL)") {
		t.Errorf("mask not substituted inside function arg: %q", res.SQL)
	}
}

// TestRewriter_ExpandStarShortMaskNoPanic guards the expand-star slice-
// bounds panic: a mask decision key shorter than a FROM table key must not
// crash fromNeedsExpansion. Here the mask is on a short-named table `t`
// referenced only in a subquery, while the outer query selects `*` from a
// longer-named table.
func TestRewriter_ExpandStarShortMaskNoPanic(t *testing.T) {
	rw := buildRewriter(t)
	m := maskPolicy("mask-c", "t", []string{"c"}, apitypes.MaskSpec{Type: apitypes.MaskNull})
	sql := "SELECT * FROM pg.public.customer_orders_history WHERE id IN (SELECT c FROM pg.a.t)"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), m)

	// Before the fix this panicked with "slice bounds out of range".
	if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql}); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}
