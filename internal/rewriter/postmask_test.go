// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
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
	// The wrapper passes the post-query masked column through raw.
	if got := normaliseSQL(res.SQL); got != "SELECT id, ssn FROM (SELECT sluice_src.id, sluice_src.ssn, sluice_src.email FROM pg.hr.employees sluice_src) employees" {
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

// In a nested SELECT an unqualified name that an outer table masks after
// the query is refused, also when a masked table of the nested SELECT has
// a column of that name. DuckDB binds the name outward where the inner
// table is not visible, such as the condition of an earlier join. The
// qualified name is served.
func TestPostMask_NestedUnqualifiedNameRefused(t *testing.T) {
	rw := buildRewriter(t)
	policies := []apitypes.Object{allowAll(0), fpeMask("employees", "ssn"),
		maskPolicy("null-other-id", "other", []string{"id"}, apitypes.MaskSpec{Type: apitypes.MaskNull})}
	const values = "(VALUES ('1'), ('2')) g(x) JOIN (VALUES (1)) h(y) ON g.x = ssn"
	for sql, refused := range map[string]bool{
		"SELECT e.id, (SELECT ssn FROM pg.hr.other o LIMIT 1) AS s FROM pg.hr.employees e":                                      true,
		"SELECT e.id, (SELECT min(g.x) FROM " + values + " JOIN pg.hr.other o ON true) AS s FROM pg.hr.employees e":             true,
		"SELECT e.id, (SELECT min(g.x) FROM " + values + ", pg.hr.other o) AS s FROM pg.hr.employees e":                         true,
		"SELECT e.id, (SELECT o.id FROM pg.hr.other o ORDER BY o.id LIMIT length(ssn)) AS s FROM pg.hr.employees e":             true,
		"SELECT e.id, (SELECT o.ssn FROM pg.hr.other o LIMIT 1) AS s FROM pg.hr.employees e":                                    false,
		"SELECT e.id, (SELECT min(g.x) FROM (VALUES ('1')) g(x) JOIN pg.hr.other o ON g.x = o.ssn) AS s FROM pg.hr.employees e": false,
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, policies...)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		switch {
		case refused && err == nil:
			t.Errorf("%s: served %q, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, res.SQL)
		case refused && pkgerr.FromError(err).Code != pkgerr.CodeMaskContext:
			t.Errorf("%s: err = %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, err)
		case !refused && err != nil:
			t.Errorf("%s: %v", sql, err)
		}
	}

	// The refusal says how to name the column of the inner table.
	sql := "SELECT e.id, (SELECT ssn FROM pg.hr.other o LIMIT 1) AS s FROM pg.hr.employees e"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, policies...)
	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err == nil || !strings.Contains(pkgerr.FromError(err).Message, "qualify") {
		t.Errorf("err = %v, want the advice to qualify the column", err)
	}
}

// A table called alias, by its name or its alias, is refused: star
// expansion would write alias.<col> for it, and DuckDB can read that as a
// select alias. A subquery called alias keeps its own star and is served.
func TestPostMask_TableCalledAliasRefused(t *testing.T) {
	rw := buildRewriter(t)
	for sql, refused := range map[string]bool{
		"SELECT e.ssn FROM pg.hr.employees e, pg.hr.other alias":                               true,
		`SELECT e.ssn FROM pg.hr.employees e JOIN pg.hr.other "ALIAS" ON true`:                 true,
		"SELECT e.ssn FROM pg.hr.employees e, pg.hr.alias":                                     true,
		"SELECT e.ssn FROM pg.hr.employees e WHERE e.id IN (SELECT id FROM pg.hr.other alias)": true,
		"SELECT e.ssn FROM pg.hr.employees e, pg.hr.alias a":                                   false,
		"SELECT e.ssn FROM pg.hr.employees e, (SELECT 1 AS one) alias":                         false,
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		switch {
		case refused && err == nil:
			t.Errorf("%s: served %q, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, res.SQL)
		case refused && rewriter.ErrorCode(err) != pkgerr.CodeMaskContext:
			t.Errorf("%s: err = %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, err)
		case !refused && err != nil:
			t.Errorf("%s: %v", sql, err)
		}
	}
}

// A star that stays in the outer select list leaves the result columns
// unknown. The refusal names the star, also when the query reads no masked
// column.
func TestPostMask_ResidualStarMessage(t *testing.T) {
	rw := buildRewriter(t)
	for _, sql := range []string{
		"SELECT * FROM (SELECT id FROM pg.hr.employees) q",
		"WITH q AS (SELECT id FROM pg.hr.employees) SELECT * FROM q",
		"SELECT e.id, g.* FROM pg.hr.employees e, generate_series(1, 2) AS g(i)",
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
		_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		if !errors.Is(err, rewriter.ErrMaskPostQueryContext) || !strings.Contains(pkgerr.FromError(err).Message, "list the columns") {
			t.Errorf("%s: err = %v, want ERR_MASK_UNSUPPORTED_CONTEXT with the advice to list the columns", sql, err)
		}
	}
}

// Two tables in one FROM mask a column of the same name after the query.
// The unqualified name could leave one of them unmasked, so it is refused.
// A wildcard mask on a table that the schema cache shows without the
// column does not count.
func TestPostMask_AmbiguousNameRefused(t *testing.T) {
	rw := buildRewriter(t)
	fpe := fpeMask("employees", "ssn").Spec.Mask
	policies := []apitypes.Object{allowAll(0), fpeMask("employees", "ssn"),
		maskPolicy("fpe-other-ssn", "other", []string{"ssn"}, fpe),
		maskPolicy("fpe-badge-all", "badge", []string{"*"}, fpe)}
	for sql, refused := range map[string]bool{
		"SELECT ssn FROM pg.hr.employees e JOIN pg.hr.other o ON o.id = e.id":               true,
		"SELECT e.id FROM pg.hr.employees e JOIN pg.hr.other o ON o.id = e.id ORDER BY ssn": true,
		"SELECT e.ssn, o.ssn FROM pg.hr.employees e JOIN pg.hr.other o ON o.id = e.id":      false,
		"SELECT ssn FROM pg.hr.employees e JOIN pg.hr.badge b ON true":                      false,
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, policies...)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		switch {
		case refused && err == nil:
			t.Errorf("%s: served %q, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, res.SQL)
		case refused && rewriter.ErrorCode(err) != pkgerr.CodeMaskContext:
			t.Errorf("%s: err = %v, want ERR_MASK_UNSUPPORTED_CONTEXT", sql, err)
		case !refused && err != nil:
			t.Errorf("%s: %v", sql, err)
		case !refused && len(res.PostMasks) == 0:
			t.Errorf("%s: no post-query mask bound in %q", sql, res.SQL)
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
