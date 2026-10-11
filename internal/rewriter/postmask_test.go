// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"fmt"
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
	if pm.ColumnIndex != 1 || pm.Column != "ssn" || pm.OutputName != "ssn" || pm.Type != apitypes.MaskFPE {
		t.Errorf("PostMask = %+v", pm)
	}
	if res.ResultColumnCount != 2 {
		t.Errorf("ResultColumnCount = %d, want 2", res.ResultColumnCount)
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

// DuckDB reads a select alias, a column position, a USING column, a nested
// join condition and DISTINCT on the raw value of a post-query masked item.
// unnest of a struct shifts the result columns. All are refused.
func TestPostMask_RejectedReferenceForms(t *testing.T) {
	rw := buildRewriter(t)
	for _, sql := range []string{
		"SELECT id, ssn AS x, x AS leak FROM pg.hr.employees",
		"SELECT id, ssn AS x, upper(x) AS leak FROM pg.hr.employees",
		"SELECT id, ssn AS x, (SELECT x) AS leak FROM pg.hr.employees",
		"SELECT id, ssn AS x FROM pg.hr.employees WHERE x = '1'",
		"SELECT id, ssn AS x FROM pg.hr.employees ORDER BY x",
		"SELECT ssn AS x, count(*) FROM pg.hr.employees GROUP BY x",
		"SELECT ssn AS x, count(*) FROM pg.hr.employees GROUP BY 1 HAVING x > '1'",
		"SELECT ssn AS x, count(*) OVER (PARTITION BY x) AS n FROM pg.hr.employees",
		"SELECT ssn AS x, count(*) OVER w AS n FROM pg.hr.employees WINDOW w AS (ORDER BY x)",
		"SELECT id, ssn AS x FROM pg.hr.employees WHERE id IN (SELECT id FROM pg.hr.other WHERE x = '1')",
		"SELECT ssn AS x, (WITH w AS (SELECT x AS y) SELECT y FROM w) AS leak FROM pg.hr.employees",
		"SELECT id, ssn FROM pg.hr.employees ORDER BY 2",
		"SELECT id, ssn FROM pg.hr.employees ORDER BY 1, 2 DESC",
		"SELECT ssn, count(*) FROM pg.hr.employees GROUP BY 1",
		"SELECT id, ssn, count(*) FROM pg.hr.employees GROUP BY ROLLUP (1, 2)",
		"SELECT id, ssn, count(*) FROM pg.hr.employees GROUP BY CUBE (2)",
		"SELECT id, ssn, count(*) FROM pg.hr.employees GROUP BY GROUPING SETS ((1, 2), ())",
		"SELECT DISTINCT ON (2) id, ssn FROM pg.hr.employees",
		`SELECT id, ssn FROM pg.hr.employees ORDER BY 2 COLLATE "C" DESC`,
		"SELECT id, ssn FROM pg.hr.employees ORDER BY id, 2 COLLATE nocase",
		"SELECT DISTINCT ON (2 COLLATE nocase) id, ssn FROM pg.hr.employees",
		"SELECT DISTINCT ssn FROM pg.hr.employees",
		"SELECT e.id FROM pg.hr.employees e JOIN pg.hr.other o USING (ssn)",
		"SELECT e.id FROM pg.hr.other o JOIN (pg.hr.badge b JOIN pg.hr.employees e ON true) USING (id)",
		"SELECT e.id FROM pg.hr.employees e NATURAL JOIN pg.hr.other o",
		"SELECT q.id FROM (SELECT o.id FROM pg.hr.other o NATURAL JOIN pg.hr.employees e) q",
		"SELECT e.id FROM pg.hr.employees e JOIN pg.hr.other o ON o.id = e.id AND e.ssn = '1' JOIN pg.hr.badge b ON b.id = o.id",
		"SELECT unnest(struct_pack(a := 1, b := 2)), ssn FROM pg.hr.employees",
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
		_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		if !errors.Is(err, rewriter.ErrMaskPostQueryContext) {
			t.Errorf("%s: err = %v, want ErrMaskPostQueryContext", sql, err)
		}
	}
}

// Positions and names that do not reach the masked item stay served.
func TestPostMask_OtherPositionsServed(t *testing.T) {
	rw := buildRewriter(t)
	for _, sql := range []string{
		"SELECT id, ssn FROM pg.hr.employees ORDER BY 1",
		"SELECT ssn, id FROM pg.hr.employees ORDER BY 2 LIMIT 1",
		"SELECT DISTINCT ON (2) ssn, id FROM pg.hr.employees",
		"SELECT ssn AS x, id AS y FROM pg.hr.employees ORDER BY y",
		"SELECT e.ssn FROM pg.hr.employees e JOIN pg.hr.other o ON o.id = e.id JOIN pg.hr.badge b ON b.id = o.id",
		"SELECT e.ssn FROM pg.hr.employees e JOIN (pg.hr.other o JOIN pg.hr.badge b USING (id)) ON o.id = e.id",
		// A CTE body of the statement cannot see its select aliases.
		"WITH t AS (SELECT id FROM pg.hr.other) SELECT e.ssn AS id FROM pg.hr.employees e JOIN t ON t.id = e.id",
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
		} else if len(res.PostMasks) != 1 {
			t.Errorf("%s: PostMasks = %+v, want 1", sql, res.PostMasks)
		}
	}
}

// queryservice checks the result shape against the select list length and
// the output name of each bound item.
func TestPostMask_ResultShapeRecorded(t *testing.T) {
	rw := buildRewriter(t)
	sql := `SELECT id, ssn AS "Tax_ID", e.ssn, e.id FROM pg.hr.employees e`
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if res.ResultColumnCount != 4 {
		t.Errorf("ResultColumnCount = %d, want 4", res.ResultColumnCount)
	}
	var got []string
	for _, pm := range res.PostMasks {
		got = append(got, fmt.Sprintf("%d:%s", pm.ColumnIndex, pm.OutputName))
	}
	if fmt.Sprint(got) != "[1:Tax_ID 2:ssn]" {
		t.Errorf("post masks = %v, want [1:Tax_ID 2:ssn]", got)
	}
}
