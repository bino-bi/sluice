// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func fpeMask(table, column string) *apitypes.ColumnMaskPolicy {
	return &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "fpe-" + column},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{table}, Columns: []string{column}}},
			}},
			Mask: apitypes.MaskSpec{Type: apitypes.MaskFPE, Args: apitypes.MaskArgs{KeyRef: "secret://env/K", Alphabet: "numeric"}},
		},
	}
}

func TestPostMask_BareTargetListRecorded(t *testing.T) {
	rw := buildRewriter(t)
	sql := "SELECT id, ssn FROM pg.hr.employees"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if len(res.PostMasks) != 1 {
		t.Fatalf("PostMasks = %+v, want 1", res.PostMasks)
	}
	pm := res.PostMasks[0]
	if pm.ColumnIndex != 1 || pm.Column != "ssn" || pm.Type != apitypes.MaskFPE {
		t.Errorf("PostMask = %+v", pm)
	}
	// The SQL is unchanged (no substitution for post-query masks).
	if got := normaliseSQL(res.SQL); got != "SELECT id, ssn FROM pg.hr.employees" {
		t.Errorf("SQL altered: %q", got)
	}
}

func TestPostMask_RejectedInWhere(t *testing.T) {
	rw := buildRewriter(t)
	sql := "SELECT id FROM pg.hr.employees WHERE ssn = '123456789'"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if !errors.Is(err, rewriter.ErrMaskPostQueryContext) {
		t.Fatalf("err = %v, want ErrMaskPostQueryContext", err)
	}
}

func TestPostMask_RejectedInTargetListExpr(t *testing.T) {
	rw := buildRewriter(t)
	sql := "SELECT id, upper(ssn) FROM pg.hr.employees"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if !errors.Is(err, rewriter.ErrMaskPostQueryContext) {
		t.Fatalf("err = %v, want ErrMaskPostQueryContext for nested expression", err)
	}
}

func TestPostMask_RejectedInGroupBy(t *testing.T) {
	rw := buildRewriter(t)
	sql := "SELECT ssn, count(*) FROM pg.hr.employees GROUP BY ssn"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if !errors.Is(err, rewriter.ErrMaskPostQueryContext) {
		t.Fatalf("err = %v, want ErrMaskPostQueryContext for GROUP BY", err)
	}
}
