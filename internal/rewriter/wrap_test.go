// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// A table with a row filter or a mask is read through a wrapper. A masked
// table's wrapper lists the cached columns in table order, masks applied.
// Every column sluice writes into a wrapper is qualified with sluice_src.

var wrapUser = &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}

func wrapRewriter(cache schema.Cache) *rewriter.Rewriter {
	return rewriter.New(rewriter.Options{
		Parser:         pgquery.New(parser.Options{}),
		DefaultCatalog: "pg",
		Salts:          goldenSaltStore{},
		Schema:         cache,
	})
}

func wrapRewrite(t *testing.T, rw *rewriter.Rewriter, sql string, policies ...apitypes.Object) (*rewriter.RewriteResult, error) {
	t.Helper()
	dec, ast := evaluate(t, sql, wrapUser, append([]apitypes.Object{allowAll(0)}, policies...)...)
	return rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: wrapUser, Raw: sql})
}

func nullMask(table, column string) *apitypes.ColumnMaskPolicy {
	return maskPolicy("null-"+column, table, []string{column}, apitypes.MaskSpec{Type: apitypes.MaskNull})
}

func sha256Mask(table, column string) *apitypes.ColumnMaskPolicy {
	return maskPolicy("hash-"+column, table, []string{column}, apitypes.MaskSpec{
		Type: apitypes.MaskHash, Args: apitypes.MaskArgs{Algorithm: apitypes.HashSHA256},
	})
}

func TestWrap_ProjectsCachedColumnsInTableOrder(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.customers": {"tenant_id", "id", "Email", "Name"},
	}))
	policies := []apitypes.Object{tenantFilter("customers"), nullMask("customers", "email")}
	const inner = `SELECT sluice_src.tenant_id, sluice_src.id, NULL AS "Email", sluice_src."Name" ` +
		`FROM pg.public.customers sluice_src WHERE sluice_src.tenant_id = $1`
	for sql, want := range map[string]string{
		"SELECT c.id FROM pg.public.customers c":            "SELECT c.id FROM (" + inner + ") c",
		"SELECT id FROM pg.public.customers":                "SELECT id FROM (" + inner + ") customers",
		"SELECT c.e FROM pg.public.customers AS c(t, i, e)": "SELECT c.e FROM (" + inner + ") c(t, i, e)",
	} {
		res, err := wrapRewrite(t, rw, sql, policies...)
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if got := normaliseSQL(res.SQL); got != want {
			t.Errorf("%s:\n got:  %s\n want: %s", sql, got, want)
		}
		if fmt.Sprint(res.Params) != "[t-1]" {
			t.Errorf("%s: params = %v, want [t-1]", sql, res.Params)
		}
		notes := slices.Sorted(slices.Values(res.Rewrites))
		if want := []string{"column-mask:pg.public.customers.Email", "row-filter-wrapped:pg.public.customers"}; !slices.Equal(notes, want) {
			t.Errorf("%s: notes = %v, want %v", sql, notes, want)
		}
	}
}

// The predicate reads the raw columns of the wrapped table, never a name
// of the outer query. Its params come first, then the mask params in
// column order, whatever order the query uses the columns in.
func TestWrap_PredicateQualifiedAndParamsFirst(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.customers": {"id", "email", "name", "tenant_id"},
	}))
	saltedHash := maskPolicy("hash-email", "customers", []string{"email"}, apitypes.MaskSpec{
		Type: apitypes.MaskHash, Args: apitypes.MaskArgs{Algorithm: apitypes.HashSHA256, SaltRef: "secret://env/SALT"},
	})
	partial := maskPolicy("partial-name", "customers", []string{"name"}, apitypes.MaskSpec{
		Type: apitypes.MaskPartial, Args: apitypes.MaskArgs{ShowFirst: 1, ShowLast: 1},
	})
	sql := "SELECT c.name, c.email, c.name AS n2 FROM pg.public.customers c"
	res, err := wrapRewrite(t, rw, sql, tenantFilter("customers"), saltedHash, partial)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "WHERE sluice_src.tenant_id = $1)") {
		t.Errorf("predicate not qualified with sluice_src or not first: %s", res.SQL)
	}
	if fmt.Sprint(res.Params) != "[t-1 golden-salt 1 * 1]" {
		t.Errorf("params = %v, want [t-1 golden-salt 1 * 1]", res.Params)
	}
	email, name := strings.Index(res.SQL, " AS email"), strings.Index(res.SQL, " AS name")
	if email < 0 || name < 0 || email > name {
		t.Errorf("want the email mask before the name mask, in table order: %s", res.SQL)
	}
	for _, col := range []string{"email", "name"} {
		if !strings.Contains(res.SQL, "sluice_src."+col) {
			t.Errorf("mask input %s not qualified with sluice_src: %s", col, res.SQL)
		}
	}
	if !strings.HasPrefix(normaliseSQL(res.SQL), "SELECT c.name, c.email, c.name AS n2 FROM (SELECT sluice_src.id, ") {
		t.Errorf("outer query changed: %s", res.SQL)
	}
}

// A mask is applied once per wrapper, so its SQL appears once per read of
// the table, however often the query names the column.
func TestWrap_MaskSQLOncePerWrapper(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.customers": {"id", "email"},
	}))
	for sql, wrappers := range map[string]int{
		"SELECT c.email, upper(c.email) AS u FROM pg.public.customers c WHERE c.email <> 'x' ORDER BY c.email": 1,
		"SELECT a.email, b.email FROM pg.public.customers a JOIN pg.public.customers b ON a.email = b.email":   2,
		"WITH x AS (SELECT c.id, c.email FROM pg.public.customers c WHERE c.email LIKE '%a%') " +
			"SELECT x.email, d.email FROM x JOIN pg.public.customers d ON d.id = x.id ORDER BY d.email": 2,
	} {
		res, err := wrapRewrite(t, rw, sql, sha256Mask("customers", "email"))
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if n := strings.Count(res.SQL, "sha256("); n != wrappers {
			t.Errorf("%s: sha256 appears %d times, want %d: %s", sql, n, wrappers, res.SQL)
		}
		if n := strings.Count(res.SQL, "FROM pg.public.customers sluice_src"); n != wrappers {
			t.Errorf("%s: %d wrappers, want %d: %s", sql, n, wrappers, res.SQL)
		}
		notes := 0
		for _, n := range res.Rewrites {
			if n == "column-mask:pg.public.customers.email" {
				notes++
			}
		}
		if notes != wrappers {
			t.Errorf("%s: %d column-mask notes, want %d: %v", sql, notes, wrappers, res.Rewrites)
		}
	}
}

// A post-query masked column passes through the wrapper raw and is masked
// in Go. A SQL mask on the same table is applied in the wrapper.
func TestWrap_PostMaskedColumnProjectedRaw(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.hr.employees": {"id", "ssn", "email"},
	}))
	sql := "SELECT id, ssn FROM pg.hr.employees"
	res, err := wrapRewrite(t, rw, sql, fpeMask("employees", "ssn"), nullMask("employees", "email"))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want := "SELECT id, ssn FROM (SELECT sluice_src.id, sluice_src.ssn, NULL AS email FROM pg.hr.employees sluice_src) employees"
	if got := normaliseSQL(res.SQL); got != want {
		t.Errorf("\n got:  %s\n want: %s", got, want)
	}
	if len(res.PostMasks) != 1 || res.PostMasks[0].ColumnIndex != 1 || res.PostMasks[0].Column != "ssn" {
		t.Errorf("PostMasks = %+v, want ssn at index 1", res.PostMasks)
	}
}

// A wildcard column pattern masks every cached column it matches, also
// the ones the query does not name.
func TestWrap_WildcardMaskResolvedAgainstCache(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.users": {"id", "ssn_last4", "name", "ssn_first2"},
	}))
	m := maskPolicy("mask-ssn", "users", []string{"ssn_*"}, apitypes.MaskSpec{Type: apitypes.MaskNull})
	res, err := wrapRewrite(t, rw, "SELECT id FROM pg.public.users", m)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want := "SELECT id FROM (SELECT sluice_src.id, NULL AS ssn_last4, sluice_src.name, NULL AS ssn_first2 FROM pg.public.users sluice_src) users"
	if got := normaliseSQL(res.SQL); got != want {
		t.Errorf("\n got:  %s\n want: %s", got, want)
	}
}

// The cache entry predates the email column. The wrapper lists only the
// cached columns, so DuckDB cannot read email at all.
func TestWrap_StaleCacheDoesNotProjectMissingColumn(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.customers": {"id", "tenant_id"},
	}))
	partial := maskPolicy("partial-email", "customers", []string{"email"}, apitypes.MaskSpec{
		Type: apitypes.MaskPartial, Args: apitypes.MaskArgs{ShowFirst: 1},
	})
	res, err := wrapRewrite(t, rw, "SELECT email FROM pg.public.customers", tenantFilter("customers"), partial)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want := "SELECT email FROM (SELECT sluice_src.id, sluice_src.tenant_id FROM pg.public.customers sluice_src WHERE sluice_src.tenant_id = $1) customers"
	if got := normaliseSQL(res.SQL); got != want {
		t.Errorf("\n got:  %s\n want: %s", got, want)
	}
}

// A masked table needs its cache entry. Without one the rewrite fails
// closed. A table with only a row filter needs none: it keeps SELECT *.
func TestWrap_MaskedTableWithoutSchemaFailsClosed(t *testing.T) {
	for name, cache := range map[string]schema.Cache{
		"no cache":      nil,
		"unknown table": staticSchema(map[string][]string{"pg.public.orders": {"id"}}),
		"no columns":    staticSchema(map[string][]string{"pg.public.customers": {}}),
	} {
		res, err := wrapRewrite(t, wrapRewriter(cache), "SELECT id FROM pg.public.customers", nullMask("customers", "email"))
		if !errors.Is(err, rewriter.ErrSchemaMissing) {
			sql := ""
			if res != nil {
				sql = res.SQL
			}
			t.Errorf("%s: err = %v (SQL %q), want ErrSchemaMissing", name, err, sql)
		}
	}

	res, err := wrapRewrite(t, wrapRewriter(nil), "SELECT id FROM pg.public.orders", tenantFilter("orders"))
	if err != nil {
		t.Fatalf("filter only: %v", err)
	}
	want := "SELECT id FROM (SELECT * FROM pg.public.orders sluice_src WHERE sluice_src.tenant_id = $1) orders"
	if got := normaliseSQL(res.SQL); got != want {
		t.Errorf("filter only:\n got:  %s\n want: %s", got, want)
	}
}

// A rule that names only a column yields a mask entry for every table it
// reads. Such a table is projected with its cached columns even when none
// matches, so a column added at the source later is not read raw.
func TestWrap_ColumnOnlyMaskProjectsEveryTable(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.orders": {"id", "total"},
	}))
	columnOnly := &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "mask-ssn-anywhere"},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Columns: []string{"ssn"}}}}},
			Mask:  apitypes.MaskSpec{Type: apitypes.MaskNull},
		},
	}
	sql := "SELECT id FROM pg.public.orders"
	dec, _ := evaluate(t, sql, wrapUser, allowAll(0), columnOnly)
	if _, ok := dec.ColumnMasks["pg.public.orders.ssn"]; !ok {
		t.Fatalf("decision masks = %v, want an entry for pg.public.orders.ssn", dec.ColumnMasks)
	}
	res, err := wrapRewrite(t, rw, sql, columnOnly)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want := "SELECT id FROM (SELECT sluice_src.id, sluice_src.total FROM pg.public.orders sluice_src) orders"
	if got := normaliseSQL(res.SQL); got != want {
		t.Errorf("\n got:  %s\n want: %s", got, want)
	}
	for _, n := range res.Rewrites {
		if strings.HasPrefix(n, "column-mask:") {
			t.Errorf("note %q for a table without a masked column", n)
		}
	}
}

// Every wrapper qualifies its columns with sluice_src. DuckDB binds a
// qualified name to an outer FROM item or struct column of that name, so
// the query may not use the name anywhere when a filter or mask applies.
func TestWrap_ReservedNameRefused(t *testing.T) {
	rw := wrapRewriter(staticSchema(map[string][]string{
		"pg.public.customers": {"id", "email", "tenant_id"},
	}))
	for _, sql := range []string{
		"SELECT sluice_src.id FROM pg.public.customers sluice_src",
		`SELECT c.id FROM (SELECT 't-1' AS tenant_id) "SLUICE_SRC", pg.public.customers c`,
		"SELECT c.id FROM (SELECT struct_pack(tenant_id := 't-1') AS sluice_src) s, pg.public.customers c",
		"WITH sluice_src AS (SELECT 1 AS x) SELECT c.id FROM pg.public.customers c, sluice_src",
		"SELECT c.id AS Sluice_Src FROM pg.public.customers c",
		"SELECT c.id FROM pg.public.customers c, (VALUES (1)) AS v(sluice_src)",
		"SELECT struct_pack(sluice_src := c.id) AS s FROM pg.public.customers c",
		`SELECT c.id FROM pg.public.customers c WHERE c.email <> '{"sluice_src": 1}'`,
	} {
		for _, p := range []apitypes.Object{tenantFilter("customers"), nullMask("customers", "email")} {
			res, err := wrapRewrite(t, rw, sql, p)
			if err == nil {
				t.Errorf("%s (%s): served %q, want ERR_UNSUPPORTED_SYNTAX", sql, p.GetObjectMeta().Name, res.SQL)
				continue
			}
			if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax || !strings.Contains(ae.Message, "sluice_src") {
				t.Errorf("%s (%s): err = %v, want ERR_UNSUPPORTED_SYNTAX naming sluice_src", sql, p.GetObjectMeta().Name, err)
			}
		}
	}
	// Without a filter or mask nothing is wrapped, so the name is free.
	sql := "SELECT c.id AS sluice_src FROM pg.public.customers c"
	if _, err := wrapRewrite(t, rw, sql, capRows(10)); err != nil {
		t.Errorf("%s with only a limit: %v", sql, err)
	}
}

// With a post-query mask in the decision, a star in a nested SELECT over
// tables without one stays. DuckDB expands it, so a stale cache entry
// drops no column and a merged join key is not listed twice.
func TestExpandStar_NestedStarWithoutPostMaskStays(t *testing.T) {
	rw := rewriter.New(rewriter.Options{Parser: pgquery.New(parser.Options{}), Schema: staticSchema(map[string][]string{
		"pg.hr.employees":  {"id", "ssn"},
		"pg.public.plain":  {"id", "k"}, // the table also has a column note
		"pg.public.plain2": {"k", "v"},
	})})
	for sql, want := range map[string]string{
		"SELECT e.id, e.ssn, x.note FROM pg.hr.employees e JOIN (SELECT * FROM pg.public.plain) x ON x.id = e.id":                                            "(SELECT * FROM pg.public.plain) x",
		"SELECT e.id, e.ssn FROM pg.hr.employees e WHERE e.id IN (SELECT k FROM (SELECT * FROM pg.public.plain a FULL JOIN pg.public.plain2 b USING (k)) x)": "(SELECT * FROM pg.public.plain a FULL JOIN pg.public.plain2 b USING (k)) x",
		"SELECT e.id, e.ssn FROM pg.hr.employees e WHERE e.id IN (SELECT a.* FROM pg.public.plain a)":                                                        "(SELECT a.* FROM pg.public.plain a)",
	} {
		dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
			continue
		}
		if !strings.Contains(res.SQL, want) {
			t.Errorf("%s: SQL = %s, want the star kept in %s", sql, res.SQL, want)
		}
	}

	// The outer select list is expanded. Both sides of a merged join would
	// list the key twice, so the star is refused.
	sql := "SELECT * FROM pg.public.plain a JOIN pg.public.plain2 b USING (k) WHERE a.id IN (SELECT id FROM pg.hr.employees)"
	dec, ast := evaluate(t, sql, &identity.UserCtx{Subject: "u"}, allowAll(0), fpeMask("employees", "ssn"))
	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
	if err == nil {
		t.Fatalf("served %q, want ERR_UNSUPPORTED_SYNTAX", res.SQL)
	}
	if code := pkgerr.FromError(err).Code; code != pkgerr.CodeUnsupportedSyntax {
		t.Fatalf("err = %v, want ERR_UNSUPPORTED_SYNTAX", err)
	}
}

// noShapeAST hides the query shape from the function gate.
type noShapeAST struct{ parser.AST }

func (noShapeAST) Shape() parser.QueryShape { return parser.QueryShape{} }

// The wrap pass does not reach a table under TABLESAMPLE. The function
// gate refuses that form, so the AST double hides the shape from it. Then
// the verifyWrapped call in Rewrite is the only thing that refuses the
// raw read.
func TestRewrite_UnwrappedTableRefused(t *testing.T) {
	rw := wrapRewriter(testSchema())
	for sql, policy := range map[string]apitypes.Object{
		"SELECT id FROM pg.hr.employees TABLESAMPLE SYSTEM (10)":  nullMask("employees", "ssn"),
		"SELECT id FROM pg.public.orders TABLESAMPLE SYSTEM (10)": tenantFilter("orders"),
	} {
		dec, ast := evaluate(t, sql, wrapUser, allowAll(0), policy)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
			AST: noShapeAST{ast}, Decision: dec, User: wrapUser, Raw: sql,
		})
		if err == nil {
			t.Errorf("%s: served %q, want ERR_UNSUPPORTED_SYNTAX", sql, res.SQL)
		} else if !errors.Is(err, rewriter.ErrUnsupportedSyntax) {
			t.Errorf("%s: err = %v, want ErrUnsupportedSyntax", sql, err)
		}
	}
}
