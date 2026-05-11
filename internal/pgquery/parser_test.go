// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
)

func newTestParser(t *testing.T) parser.Parser {
	t.Helper()
	return pgquery.New(parser.Options{})
}

func TestParseSimpleSelect(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT 1 FROM orders")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ast.Statement() != parser.StmtSelect {
		t.Errorf("Statement = %v; want SELECT", ast.Statement())
	}
	tables := ast.Tables()
	if len(tables) != 1 || tables[0].Table != "orders" {
		t.Fatalf("Tables = %v; want [orders]", tables)
	}
	if ast.Source() != "SELECT 1 FROM orders" {
		t.Errorf("Source not preserved: %q", ast.Source())
	}
	if ast.Fingerprint() == "" {
		t.Error("Fingerprint is empty")
	}
}

func TestParseQualifiedTable(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT * FROM pg.public.orders")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tables := ast.Tables()
	if len(tables) != 1 {
		t.Fatalf("Tables = %v; want 1", tables)
	}
	want := parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}
	if tables[0] != want {
		t.Errorf("Tables[0] = %+v; want %+v", tables[0], want)
	}
	if cats := ast.Catalogs(); !reflect.DeepEqual(cats, []string{"pg"}) {
		t.Errorf("Catalogs = %v; want [pg]", cats)
	}
}

func TestParseJoinShape(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT * FROM a JOIN b ON a.x = b.x JOIN c USING (y) WHERE a.z > 0")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if n := len(ast.Tables()); n != 3 {
		t.Errorf("Tables count = %d; want 3", n)
	}
	shape := ast.Shape()
	if shape.Joins != 2 {
		t.Errorf("Joins = %d; want 2", shape.Joins)
	}
	if !shape.HasWhere {
		t.Error("HasWhere = false; want true")
	}
	if !shape.HasSelectStar {
		t.Error("HasSelectStar = false; want true")
	}
}

func TestParseCTEExcludesRefs(t *testing.T) {
	ctx := context.Background()
	sql := "WITH recent AS (SELECT * FROM orders) SELECT * FROM recent"
	ast, err := newTestParser(t).Parse(ctx, sql)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tables := ast.Tables()
	// "recent" is a CTE and should not show up; "orders" should.
	for _, tr := range tables {
		if tr.Table == "recent" {
			t.Errorf("CTE name leaked into Tables: %+v", tr)
		}
	}
	found := false
	for _, tr := range tables {
		if tr.Table == "orders" {
			found = true
		}
	}
	if !found {
		t.Errorf("orders not in Tables: %+v", tables)
	}
	if !ast.Shape().HasCTE {
		t.Error("HasCTE = false; want true")
	}
}

func TestParseUnionHasUnionFlag(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT * FROM a UNION SELECT * FROM b")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !ast.Shape().HasUnion {
		t.Error("HasUnion = false; want true")
	}
}

func TestParseLimitCapture(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT * FROM orders LIMIT 10")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	shape := ast.Shape()
	if !shape.HasLimit {
		t.Error("HasLimit = false; want true")
	}
	if shape.LimitValue != 10 {
		t.Errorf("LimitValue = %d; want 10", shape.LimitValue)
	}
}

func TestParseAggregateDetected(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT COUNT(*) FROM orders")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !ast.Shape().IsAggregate {
		t.Error("IsAggregate = false; want true")
	}
}

func TestParseMultipleStatements(t *testing.T) {
	ctx := context.Background()
	_, err := newTestParser(t).Parse(ctx, "SELECT 1; SELECT 2;")
	if !errors.Is(err, parser.ErrMultipleStatements) {
		t.Fatalf("err = %v; want ErrMultipleStatements", err)
	}
}

func TestParseSyntaxError(t *testing.T) {
	ctx := context.Background()
	_, err := newTestParser(t).Parse(ctx, "SELEC * FROM orders")
	if !errors.Is(err, parser.ErrSyntax) {
		t.Fatalf("err = %v; want ErrSyntax", err)
	}
	var pe *parser.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("err not *ParseError: %T", err)
	}
}

func TestParseInputTooLarge(t *testing.T) {
	p := pgquery.New(parser.Options{MaxSQLBytes: 16})
	_, err := p.Parse(context.Background(), "SELECT * FROM a_table_with_a_quite_long_name_to_exceed")
	if !errors.Is(err, parser.ErrInputTooLarge) {
		t.Fatalf("err = %v; want ErrInputTooLarge", err)
	}
}

func TestParseRejectsDDL(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "CREATE TABLE foo (x int)")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if kind := ast.Statement(); kind != parser.StmtDDL {
		t.Errorf("Statement = %v; want DDL", kind)
	}
	if ast.Statement().IsReadOnly() {
		t.Error("DDL marked as read-only")
	}
}

func TestParseInsertUpdateDelete(t *testing.T) {
	ctx := context.Background()
	cases := map[string]parser.StmtKind{
		"INSERT INTO t VALUES (1)":                   parser.StmtInsert,
		"UPDATE t SET x = 1 WHERE y = 2":             parser.StmtUpdate,
		"DELETE FROM t WHERE y = 2":                  parser.StmtDelete,
		"COPY t FROM '/tmp/x.csv' DELIMITER ',' CSV": parser.StmtCopy,
	}
	for sql, want := range cases {
		t.Run(sql, func(t *testing.T) {
			ast, err := newTestParser(t).Parse(ctx, sql)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := ast.Statement(); got != want {
				t.Errorf("Statement = %v; want %v", got, want)
			}
			if ast.Statement().IsReadOnly() {
				t.Errorf("%v marked as read-only", want)
			}
		})
	}
}

func TestFingerprintIsStableForParameterizedSelects(t *testing.T) {
	p := newTestParser(t)
	a, err := p.Fingerprint("SELECT * FROM orders WHERE id = 1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Fingerprint("SELECT * FROM orders WHERE id = 999")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("fingerprints differ across literal change: %s vs %s", a, b)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	ctx := context.Background()
	ast, err := newTestParser(t).Parse(ctx, "SELECT 1 FROM orders")
	if err != nil {
		t.Fatal(err)
	}
	c := ast.Clone()
	if c == nil {
		t.Fatal("Clone returned nil")
	}
	if c.Source() != ast.Source() {
		t.Errorf("Clone source mismatch: %q vs %q", c.Source(), ast.Source())
	}
	if c.Fingerprint() != ast.Fingerprint() {
		t.Errorf("Clone fingerprint mismatch")
	}
}

func TestDeparseRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := newTestParser(t)
	for _, sql := range []string{
		"SELECT id, name FROM orders WHERE id > 0",
		"SELECT COUNT(*) FROM orders",
		"SELECT * FROM a JOIN b ON a.x = b.x",
	} {
		t.Run(sql, func(t *testing.T) {
			ast, err := p.Parse(ctx, sql)
			if err != nil {
				t.Fatal(err)
			}
			out, err := p.Deparse(ctx, ast)
			if err != nil {
				t.Fatalf("Deparse: %v", err)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatal("Deparse produced empty output")
			}
			// Re-parse the deparsed SQL and confirm it has the same tables.
			ast2, err := p.Parse(ctx, out)
			if err != nil {
				t.Fatalf("re-parse of %q: %v", out, err)
			}
			if !reflect.DeepEqual(ast2.Tables(), ast.Tables()) {
				t.Errorf("tables differ after round-trip:\n  orig: %+v\n  new:  %+v", ast.Tables(), ast2.Tables())
			}
		})
	}
}

func TestParserVersion(t *testing.T) {
	v := pgquery.ParserVersion()
	if v == "" || v == "unknown" {
		t.Errorf("ParserVersion = %q; want non-empty, non-unknown", v)
	}
}
