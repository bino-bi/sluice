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

// pg_query reads any non-ASCII byte as part of an identifier, DuckDB reads
// Unicode spaces as separators. A statement served unchanged must mean the
// same to both, so such identifiers are refused unless quoted.
func TestParseRejectsNonLetterUnicodeInIdentifiers(t *testing.T) {
	ctx := context.Background()
	p := newTestParser(t)

	// Built from code points so the source stays plain ASCII.
	nbsp, emSpace, ideoSpace := string(rune(0x00a0)), string(rune(0x2003)), string(rune(0x3000))
	zeroWidth, bom := string(rune(0x200b)), string(rune(0xfeff))

	rejected := []string{
		"SELECT 1 FROM orders a" + nbsp + "b",
		"SELECT 1 FROM (SELECT 1) q" + emSpace + "(c)",
		"SELECT" + ideoSpace + "id FROM orders",
		"SELECT id FROM orders o" + zeroWidth,
		"SELECT id FROM orders x" + bom,
		"SELECT id FROM orders " + string([]byte{0xff}),
	}
	for _, sql := range rejected {
		_, err := p.Parse(ctx, sql)
		var pe *parser.ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) error = %v, want *parser.ParseError", sql, err)
		}
	}

	accepted := []string{
		"SELECT gr" + string(rune(0x00f6)) + string(rune(0x00df)) + "e FROM orders",
		"SELECT " + string(rune(0x540d)) + string(rune(0x524d)) + " FROM orders",
		"SELECT \"a" + nbsp + "b\" FROM orders",
		"SELECT 'a" + nbsp + "b' AS s FROM orders",
		"SELECT id FROM orders -- note" + nbsp + "with space",
		"SELECT id /* " + ideoSpace + " */ FROM orders",
	}
	for _, sql := range accepted {
		if _, err := p.Parse(ctx, sql); err != nil {
			t.Errorf("Parse(%q): %v", sql, err)
		}
	}
}

// EXPLAIN ANALYZE runs its body, so EXPLAIN is only read-only over a
// SELECT. Any other body keeps the kind of that body.
func TestStatementKindOfExplainFollowsItsBody(t *testing.T) {
	cases := []struct {
		sql  string
		want parser.StmtKind
	}{
		{"EXPLAIN SELECT 1 FROM t", parser.StmtExplain},
		{"EXPLAIN ANALYZE SELECT 1 FROM t", parser.StmtExplain},
		{"EXPLAIN ANALYZE CREATE TABLE x AS SELECT * FROM t", parser.StmtDDL},
		{"EXPLAIN ANALYZE INSERT INTO x SELECT * FROM t", parser.StmtInsert},
		{"EXPLAIN ANALYZE UPDATE x SET a = 1", parser.StmtUpdate},
		{"EXPLAIN ANALYZE DELETE FROM x", parser.StmtDelete},
	}
	for _, tc := range cases {
		ast, err := newTestParser(t).Parse(context.Background(), tc.sql)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.sql, err)
		}
		if got := ast.Statement(); got != tc.want {
			t.Errorf("%q: kind = %s, want %s", tc.sql, got, tc.want)
		}
		if tc.want != parser.StmtExplain && ast.Statement().IsReadOnly() {
			t.Errorf("%q must not count as read-only", tc.sql)
		}
	}
}

// A statement nested deeper than the C stack allows kills the process
// inside pg_query. These shapes did that, or would do it in the deparser.
// Each count is above the guard limit but below pg_query's own crash
// depth, so a guard regression fails the assertion instead of segfaulting
// the test binary.
func TestParseRefusesDeepStatements(t *testing.T) {
	rep := strings.Repeat
	const chain = 3000 // > maxExprDepth/maxJoinDepth, < operator/JOIN crash (~4500)
	const set = 4500   // > maxSetDepth, < set-operation crash (~8000)
	const heavy = 600  // > maxSubqueryDepth, < subquery/CASE crash (~1400)
	const light = 2500 // > maxExprDepth, < paren/function/array crash (~4500)
	const quad = 80    // M=K; true depth ~6400 >> maxExprDepth, pg does not crash at this size
	deep := map[string]string{
		"binary operators": "SELECT 1" + rep("+1", chain),
		"concatenation":    "SELECT 'a'" + rep("||'a'", chain),
		"casts":            "SELECT 1" + rep("::int", chain),
		"collate":          "SELECT 'a'" + rep(` COLLATE "C"`, chain),
		"at time zone":     "SELECT now()" + rep(" AT TIME ZONE 'UTC'", chain),
		"NOT":              "SELECT " + rep("NOT ", chain) + "true",
		// Postfix predicate tests chain to arbitrary depth with no bracket
		// and no infix operator token.
		"IS NULL chain":      "SELECT x" + rep(" IS NULL", chain) + " FROM t",
		"IS JSON chain":      "SELECT x" + rep(" IS JSON", chain) + " FROM t",
		"NOTNULL chain":      "SELECT x" + rep(" NOTNULL", chain) + " FROM t",
		"IN chain":           "SELECT x" + rep(" IN (1)", chain) + " FROM t",
		"joins":              "SELECT 1 FROM t" + rep(" JOIN t ON true", chain),
		"joins with AND":     "SELECT 1 FROM t" + rep(" JOIN t ON true AND true", chain),
		"set operations":     "SELECT 1, 2" + rep(" UNION ALL SELECT 1, 2", set),
		"operands in parens": "SELECT 1" + rep("+(1)", chain),
		// Nested brackets each carrying a trailing operator chain: true depth
		// is the product of the nesting and the per-level operator count, not
		// their sum.
		"bracket-operator product": "SELECT " + rep("(", quad) + "1" + rep(")"+rep("+1", quad), quad),
		// Heavy brackets (subquery, CASE) crash earliest, capped tightest.
		"subqueries":      "SELECT " + rep("(SELECT ", heavy) + "1" + rep(")", heavy),
		"FROM subqueries": "SELECT 1 FROM " + rep("(SELECT 1 FROM ", heavy) + "t" + rep(") x", heavy),
		"CASE":            "SELECT " + rep("CASE WHEN true THEN ", heavy) + "1" + rep(" END", heavy),
		// Light brackets (function args, arrays) crash far deeper, capped at
		// the expression-depth limit.
		"function calls":  "SELECT " + rep("abs(", light) + "1" + rep(")", light),
		"arrays":          "SELECT " + rep("ARRAY[", light) + "1" + rep("]", light),
		"in another kind": "INSERT INTO t SELECT 1" + rep("+1", chain),
	}
	p := pgquery.New(parser.Options{MaxSQLBytes: 1 << 24})
	for name, sql := range deep {
		t.Run(name, func(t *testing.T) {
			_, err := p.Parse(context.Background(), sql)
			if !errors.Is(err, parser.ErrTooDeep) {
				t.Fatalf("Parse error = %.80v; want ErrTooDeep", err)
			}
			if _, err := p.Fingerprint(sql); !errors.Is(err, parser.ErrTooDeep) {
				t.Fatalf("Fingerprint error = %.80v; want ErrTooDeep", err)
			}
		})
	}
}

// Long statements are not deep statements. Lists and AND/OR chains are flat
// in the parse tree.
func TestParseAcceptsLongFlatStatements(t *testing.T) {
	rep := strings.Repeat
	flat := map[string]string{
		"IN list":            "SELECT 1 FROM t WHERE a IN (1" + rep(", 1", 50000) + ")",
		"OR chain":           "SELECT 1 FROM t WHERE a = 1" + rep(" OR a = 1", 20000),
		"AND chain":          "SELECT 1 FROM t WHERE a = 1" + rep(" AND b <> 2", 20000),
		"CASE branches":      "SELECT CASE" + rep(" WHEN a = 1 THEN 'x'", 20000) + " ELSE 'y' END FROM t",
		"select list":        "SELECT a" + rep(", sum(b) + 1 AS c", 20000) + " FROM t",
		"VALUES rows":        "SELECT * FROM (VALUES (1, 'a')" + rep(", (2, 'b')", 20000) + ") v(x, y)",
		"BETWEEN chain":      "SELECT 1 FROM t WHERE a BETWEEN 1 AND 2" + rep(" OR a BETWEEN 3 AND 4", 20000),
		"1000 operators":     "SELECT 1" + rep(" + 1", 1000),
		"1000 concat":        "SELECT 'a'" + rep(" || 'b'", 1000),
		"1000 joins":         "SELECT 1 FROM t" + rep(" JOIN t ON true", 1000),
		"2000 union arms":    "SELECT a, b FROM t WHERE a = 1" + rep(" UNION ALL SELECT a, b FROM t WHERE a = 1", 1999),
		"400 subqueries":     "SELECT " + rep("(SELECT ", 400) + "1" + rep(")", 400),
		"400 function calls": "SELECT " + rep("abs(", 400) + "1" + rep(")", 400),
	}
	p := pgquery.New(parser.Options{MaxSQLBytes: 1 << 24})
	for name, sql := range flat {
		t.Run(name, func(t *testing.T) {
			// Under -race each new protobuf message costs an atomic store, so
			// these trees put millions of sync events into one goroutine. That
			// makes the race detector report this goroutine racing with
			// itself. The goroutine shares no mutable data, so its sync
			// events carry no information. Memory accesses are still checked.
			raceDisableSync()
			defer raceEnableSync()
			ast, err := p.Parse(context.Background(), sql)
			if err != nil {
				t.Fatalf("Parse: %.200v", err)
			}
			if _, err := p.Deparse(context.Background(), ast); err != nil {
				t.Fatalf("Deparse: %.200v", err)
			}
		})
	}
}

// A FROM alias that DuckDB reads as a keyword is refused: pg_query reads it
// as an alias and deparses it bare. A join keyword restructures the join
// in DuckDB and skips the row filter and mask, and qualify(x) becomes a
// QUALIFY clause on the raw column x.
func TestParseRefusesJoinKeywordAliases(t *testing.T) {
	p := newTestParser(t)
	for _, sql := range []string{
		`SELECT 1 FROM customers "semi" JOIN orders ON true`,
		`SELECT 1 FROM customers AS anti JOIN orders ON true`,
		`SELECT 1 FROM customers asof JOIN orders ON true`,
		`SELECT 1 FROM (SELECT 1) "positional" JOIN orders ON true`,
		`SELECT 1 FROM orders o JOIN customers "semi" ON true`,
		`SELECT 1 FROM customers c, generate_series(1, 1) AS qualify(email)`,
		`SELECT 1 FROM customers c CROSS JOIN (SELECT 1) AS qualify(x)`,
		`SELECT 1 FROM customers AS pivot`,
	} {
		if _, err := p.Parse(context.Background(), sql); !errors.Is(err, parser.ErrAliasKeyword) {
			t.Errorf("%q: err = %v, want ErrAliasKeyword", sql, err)
		}
	}
	// A column named or aliased like a join keyword is fine; only FROM
	// aliases restructure a join. pg_query quotes an upper-case alias, and
	// DuckDB reads a quoted name as an alias.
	for _, sql := range []string{
		`SELECT 1 AS semi FROM customers`,
		`SELECT semi FROM customers`,
		`SELECT 1 FROM customers c JOIN orders o ON true`,
		`SELECT 1 FROM customers c, generate_series(1, 1) AS "QUALIFY"(x)`,
		`SELECT 1 FROM customers "ASOF" JOIN orders ON true`,
		`SELECT 1 FROM customers "Semi" JOIN orders ON true`,
	} {
		if _, err := p.Parse(context.Background(), sql); err != nil {
			t.Errorf("%q: unexpected error %v", sql, err)
		}
	}
}

// pg_query cuts identifiers at 63 bytes and DuckDB does not. A policy on
// the full name would not match the name policy sees, and a CTE named with
// the first 63 bytes would hide the table. Such names are refused.
func TestParseRefusesIdentifiersOver63Bytes(t *testing.T) {
	p := newTestParser(t)
	a := func(n int) string { return strings.Repeat("a", n) }
	oe := string(rune(0x00f6)) // two bytes in UTF-8
	long := "payroll_salaries_by_employee_and_month_from_hr_system_snapshot_2026"

	refused := []string{
		"SELECT 1 FROM " + a(64),
		"SELECT 1 FROM shop.main." + long,
		"SELECT " + a(64) + " FROM t",
		"SELECT 1 AS " + a(64) + " FROM t",
		`SELECT 1 FROM "` + a(64) + `"`,
		`SELECT 1 FROM "` + a(62) + `""a"`, // decodes to 64 bytes
		"SELECT 1 FROM " + strings.Repeat(oe, 32),
		`SELECT 1 FROM U&"` + a(64) + `"`,
		`SELECT 1 FROM U&"` + a(60) + `\0061"`, // the escape counts as written
		"WITH " + long[:63] + "x AS (SELECT 1 AS id) SELECT p.* FROM t, " + long + " p",
	}
	for _, sql := range refused {
		if _, err := p.Parse(context.Background(), sql); !errors.Is(err, parser.ErrIdentifierTooLong) {
			t.Errorf("Parse(%.60q): err = %v, want ErrIdentifierTooLong", sql, err)
		}
		if _, err := p.Fingerprint(sql); !errors.Is(err, parser.ErrIdentifierTooLong) {
			t.Errorf("Fingerprint(%.60q): err = %v, want ErrIdentifierTooLong", sql, err)
		}
	}

	accepted := []string{
		"SELECT 1 FROM " + a(63),
		`SELECT 1 FROM "` + a(63) + `"`,
		`SELECT 1 FROM "` + a(61) + `""a"`, // decodes to 63 bytes
		"SELECT 1 FROM " + strings.Repeat(oe, 31) + "a",
		`SELECT 1 FROM U&"` + a(63) + `"`,
		`SELECT 1 FROM u&"` + a(63) + `"`,
		"SELECT '" + a(100) + "' AS s FROM t",
		"SELECT 1 FROM t -- " + a(100),
	}
	for _, sql := range accepted {
		if _, err := p.Parse(context.Background(), sql); err != nil {
			t.Errorf("Parse(%.60q): %v", sql, err)
		}
	}
}

// The query.* values of reject rules and conditions. A value describes the
// whole statement: every SELECT of a set operation, the ORDER BY and LIMIT
// of the statement, a WITH and a join at any depth.
func TestShapeValues(t *testing.T) {
	type want struct {
		star, where, agg, orderBy, limit, cte, recursive, union bool
		limitValue                                              int64
		joins                                                   int
		whereCols, groupCols                                    string
	}
	cases := []struct {
		sql  string
		want want
	}{
		{"SELECT id FROM a WHERE x = 1 ORDER BY id LIMIT 5",
			want{where: true, orderBy: true, limit: true, limitValue: 5, whereCols: "x"}},
		// Every SELECT of the UNION must have a WHERE and aggregate.
		{"SELECT id FROM a WHERE x = 1 UNION SELECT id FROM b",
			want{union: true, whereCols: "x"}},
		{"SELECT id FROM a WHERE x = 1 UNION SELECT id FROM b WHERE y = 2",
			want{union: true, where: true, whereCols: "x,y"}},
		{"SELECT count(*) FROM a UNION ALL SELECT id FROM b",
			want{union: true}},
		{"SELECT count(*) FROM a GROUP BY g UNION ALL SELECT max(id) FROM b GROUP BY h",
			want{union: true, agg: true, groupCols: "g,h"}},
		// A star in any SELECT of the UNION, not only the first.
		{"SELECT id FROM a UNION SELECT * FROM b",
			want{union: true, star: true}},
		{"SELECT id FROM a UNION SELECT id FROM b UNION SELECT c.* FROM c",
			want{union: true, star: true}},
		// ORDER BY and LIMIT of the statement, not of one SELECT in parentheses.
		{"SELECT id FROM a UNION SELECT id FROM b ORDER BY id LIMIT 7",
			want{union: true, orderBy: true, limit: true, limitValue: 7}},
		{"(SELECT id FROM a ORDER BY id LIMIT 3) UNION SELECT id FROM b",
			want{union: true}},
		// A WITH of the whole statement and a WITH in a subquery.
		{"WITH w AS (SELECT id FROM a) SELECT id FROM w UNION SELECT id FROM b",
			want{union: true, cte: true}},
		{"SELECT id FROM (WITH RECURSIVE r AS (SELECT 1 AS id) SELECT id FROM r) s",
			want{cte: true, recursive: true}},
		// Joins: the keyword, a comma, and both in a subquery.
		{"SELECT a.id FROM a, b", want{joins: 1}},
		{"SELECT a.id FROM a JOIN b ON a.id = b.id, c", want{joins: 2}},
		{"SELECT id FROM (SELECT a.id FROM a JOIN b USING (id) JOIN c USING (id)) s", want{joins: 2}},
		{"SELECT id FROM a WHERE id IN (SELECT b.id FROM b, c)", want{where: true, joins: 1}},
		{"SELECT a.id FROM a JOIN b USING (id) UNION SELECT c.id FROM c JOIN d USING (id)", want{union: true, joins: 2}},
		// A star in a subquery is not the result of the statement.
		{"SELECT id FROM (SELECT * FROM a) s", want{}},
		{"EXPLAIN SELECT id FROM a UNION SELECT * FROM b", want{union: true, star: true}},
	}
	p := newTestParser(t)
	for _, tc := range cases {
		ast, err := p.Parse(context.Background(), tc.sql)
		if err != nil {
			t.Errorf("%s: %v", tc.sql, err)
			continue
		}
		s := ast.Shape()
		got := want{
			star: s.HasSelectStar, where: s.HasWhere, agg: s.IsAggregate, orderBy: s.HasOrderBy,
			limit: s.HasLimit, cte: s.HasCTE, recursive: s.IsRecursiveCTE, union: s.HasUnion,
			limitValue: s.LimitValue, joins: s.Joins,
			whereCols: strings.Join(s.WhereColumns, ","), groupCols: strings.Join(s.GroupByColumns, ","),
		}
		if got != tc.want {
			t.Errorf("%s:\n got  %+v\n want %+v", tc.sql, got, tc.want)
		}
	}
}
