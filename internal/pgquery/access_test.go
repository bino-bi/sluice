// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"slices"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

func shapeOf(t *testing.T, sql string) parser.QueryShape {
	t.Helper()
	ast, err := newTestParser(t).Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	return ast.Shape()
}

func TestAccessedColumns_DeepWalk(t *testing.T) {
	s := shapeOf(t, "SELECT id, email FROM pg.hr.employees WHERE dept = 'eng' GROUP BY dept ORDER BY id")
	for _, want := range []string{"id", "email", "dept"} {
		if !slices.Contains(s.AccessedColumns, want) {
			t.Errorf("AccessedColumns %v missing %q", s.AccessedColumns, want)
		}
	}
}

func TestAccessedColumns_Subquery(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.a WHERE tenant IN (SELECT tenant FROM pg.b WHERE secret_flag = true)")
	if !slices.Contains(s.AccessedColumns, "secret_flag") {
		t.Errorf("subquery column not surfaced: %v", s.AccessedColumns)
	}
}

func TestComparisons_ColumnVsLiteral(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE country = 'de' AND age >= 18")
	want := []parser.Comparison{
		{Column: "country", Op: "=", Value: "de"},
		{Column: "age", Op: ">=", Value: "18"},
	}
	for _, w := range want {
		if !hasComparison(s.Comparisons, w) {
			t.Errorf("missing comparison %+v in %+v", w, s.Comparisons)
		}
	}
}

func TestComparisons_ReversedOperands(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE 18 < age")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "age", Op: ">", Value: "18"}) {
		t.Errorf("reversed operand not normalised: %+v", s.Comparisons)
	}
}

func TestComparisons_InList(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE region IN ('eu', 'us')")
	for _, v := range []string{"eu", "us"} {
		if !hasComparison(s.Comparisons, parser.Comparison{Column: "region", Op: "in", Value: v}) {
			t.Errorf("IN literal %q missing: %+v", v, s.Comparisons)
		}
	}
}

func TestComparisons_IsNull(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE deleted_at IS NULL")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "deleted_at", Op: "isnull"}) {
		t.Errorf("IS NULL not recorded: %+v", s.Comparisons)
	}
}

func TestComparisons_Like(t *testing.T) {
	s := shapeOf(t, "SELECT id FROM pg.t WHERE name LIKE 'A%'")
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "name", Op: "like", Value: "A%"}) {
		t.Errorf("LIKE not recorded: %+v", s.Comparisons)
	}
}

func hasComparison(cs []parser.Comparison, want parser.Comparison) bool {
	return slices.Contains(cs, want)
}

// A form that reads columns of a table without naming them. DuckDB
// resolves names without regard to case, also when quoted.
func TestAccess_AllColumns(t *testing.T) {
	all := []string{
		"SELECT * FROM pg.hr.people",
		"SELECT p.* FROM pg.hr.people p",
		"SELECT id FROM (SELECT * FROM pg.hr.people) s",
		"SELECT id FROM pg.hr.a UNION SELECT * FROM pg.hr.people",
		"SELECT s.* FROM (SELECT 1 AS x) s JOIN pg.hr.people p ON true",
		"SELECT (SELECT c.* FROM (SELECT 1 AS x) y) FROM pg.hr.people c",
		// The table, its alias or a join alias as a row value.
		"SELECT p FROM pg.hr.people p",
		`SELECT "P" FROM pg.hr.people p`,
		`SELECT p FROM pg.hr.people "P"`,
		`SELECT "PEOPLE" FROM pg.hr.people`,
		"SELECT hr.people FROM pg.hr.people",
		"SELECT to_json(people) FROM pg.hr.people",
		`SELECT ("P").ssn FROM pg.hr.people p`,
		"SELECT j FROM (pg.hr.people p JOIN pg.hr.badges b ON b.id = p.id) AS j",
		// Columns under a new name or a position.
		"SELECT c FROM pg.hr.people p(a, b, c)",
		"SELECT j.c4 FROM (pg.hr.people p JOIN pg.hr.badges b ON b.id = p.id) AS j(c1, c2, c3, c4)",
		"SELECT #4 FROM pg.hr.people",
		"SELECT id FROM pg.hr.people WHERE #3 = 'de'",
		"SELECT id FROM pg.hr.people NATURAL JOIN pg.hr.badges",
	}
	for _, sql := range all {
		if !shapeOf(t, sql).AccessesAllColumns {
			t.Errorf("%s: AccessesAllColumns = false", sql)
		}
	}
	named := []string{
		"SELECT id, name FROM pg.hr.people p",
		"SELECT p.id FROM pg.hr.people p",
		"SELECT count(*) FROM pg.hr.people",
		"SELECT s.id FROM (SELECT id FROM pg.hr.people) s",
		// A star over a subquery or CTE that names its columns: the wrapper
		// BI tools put around a query.
		"SELECT * FROM (SELECT id, name FROM pg.hr.people) AS virtual_table LIMIT 100",
		"WITH t AS (SELECT id, name FROM pg.hr.people) SELECT * FROM t",
		"SELECT s FROM (SELECT id, name FROM pg.hr.people) s",
		// EXISTS returns no column of its subquery.
		"SELECT a.id FROM pg.hr.a a WHERE EXISTS (SELECT * FROM pg.hr.people p WHERE p.id = a.id)",
		// A column of a table whose alias is also a column name elsewhere.
		"SELECT x.id FROM pg.hr.people x JOIN pg.hr.badges b ON b.id = x.id",
		"SELECT j.id FROM (pg.hr.people p JOIN pg.hr.badges b ON b.id = p.id) AS j",
	}
	for _, sql := range named {
		if shapeOf(t, sql).AccessesAllColumns {
			t.Errorf("%s: AccessesAllColumns = true", sql)
		}
	}
}

// Every expression form counts as an access to the column in it. Names
// are reported in lower case.
func TestAccessedColumns_EveryForm(t *testing.T) {
	for _, sql := range []string{
		"SELECT greatest(ssn, '') FROM pg.t",
		"SELECT ARRAY[ssn] FROM pg.t",
		"SELECT ROW(ssn, 1) FROM pg.t",
		"SELECT ssn IS TRUE FROM pg.t",
		`SELECT ssn COLLATE "C" FROM pg.t`,
		"SELECT (ssn)[1] FROM pg.t",
		"SELECT count(*) FILTER (WHERE ssn = 'x') FROM pg.t",
		"SELECT string_agg(name, ',' ORDER BY ssn) FROM pg.t",
		"SELECT sum(1) OVER (PARTITION BY ssn) FROM pg.t",
		"SELECT sum(1) OVER w FROM pg.t WINDOW w AS (ORDER BY ssn)",
		"SELECT DISTINCT ON (ssn) id FROM pg.t",
		"SELECT id FROM pg.t, generate_series(1, ssn) g",
		"SELECT id FROM pg.a JOIN pg.b USING (ssn)",
		`SELECT id FROM pg.a JOIN pg.b USING ("SSN")`,
		"SELECT id FROM pg.t ORDER BY 1 LIMIT ssn",
		"SELECT x FROM (VALUES (1)) v(x) WHERE x IN (SELECT ssn FROM pg.t)",
		`SELECT "SSN" FROM pg.t`,
		`SELECT p."Ssn" FROM pg.t p`,
	} {
		s := shapeOf(t, sql)
		if !slices.Contains(s.AccessedColumns, "ssn") && !slices.Contains(s.AccessedColumns, "p.ssn") {
			t.Errorf("%s: AccessedColumns %v misses ssn", sql, s.AccessedColumns)
		}
	}
}

// A column in a condition that is not a plain comparison with a literal
// cannot be read as (column, op, value), so it is reported on its own.
func TestAccess_UnreadPredicateColumns(t *testing.T) {
	unread := []string{
		"SELECT id FROM pg.t WHERE lower(country) = 'de'",
		`SELECT id FROM pg.t WHERE lower("Country") = 'de'`,
		"SELECT id FROM pg.t WHERE country || '' = 'de'",
		"SELECT id FROM pg.t WHERE country BETWEEN 'de' AND 'de'",
		"SELECT id FROM pg.t WHERE country = ANY (ARRAY['de'])",
		"SELECT id FROM pg.t WHERE country IN (SELECT 'de')",
		"SELECT id FROM pg.t WHERE country IN ('fr', lower('DE'))",
		"SELECT id FROM pg.t WHERE country = home_country",
		"SELECT id FROM pg.t WHERE NOT (country <> 'de')",
		"SELECT id FROM pg.t WHERE country NOT IN ('fr')",
		"SELECT id FROM pg.t WHERE country NOT LIKE 'fr'",
		"SELECT id FROM pg.t WHERE country NOT ILIKE 'fr'",
		"SELECT id FROM pg.t WHERE (country <> 'de') = false",
		"SELECT id FROM pg.t WHERE CASE WHEN country = 'de' THEN true ELSE false END",
		"SELECT id FROM pg.t WHERE CASE country WHEN 'de' THEN true ELSE false END",
		// A cast can change the value that is compared.
		`SELECT id FROM pg.t WHERE country = '\x64\x65'::BLOB::VARCHAR`,
		"SELECT id FROM pg.t WHERE country IN ('de'::VARCHAR)",
		// A negation around a subquery or an aggregate FILTER.
		"SELECT id FROM pg.t WHERE NOT EXISTS (SELECT 1 WHERE country <> 'de')",
		"SELECT id FROM pg.t WHERE 0 = (SELECT count(*) WHERE country <> 'de')",
		"SELECT id FROM pg.t GROUP BY id HAVING count(*) FILTER (WHERE country <> 'de') = 0",
		"SELECT lower(country) = 'de' FROM pg.t",
		"SELECT id FROM pg.t ORDER BY country = 'de'",
		"SELECT id FROM pg.a JOIN pg.b USING (country)",
		"SELECT id FROM pg.a JOIN pg.b ON lower(a.country) = 'de'",
		"SELECT count(*) FILTER (WHERE upper(country) = 'DE') FROM pg.t",
		"SELECT id FROM pg.t GROUP BY id HAVING max(country) = 'de'",
	}
	for _, sql := range unread {
		s := shapeOf(t, sql)
		if !slices.Contains(s.UnreadPredicateColumns, "country") && !slices.Contains(s.UnreadPredicateColumns, "a.country") {
			t.Errorf("%s: UnreadPredicateColumns %v misses country", sql, s.UnreadPredicateColumns)
		}
	}
	read := []string{
		"SELECT id FROM pg.t WHERE country = 'fr'",
		`SELECT id FROM pg.t WHERE "COUNTRY" = 'fr'`,
		"SELECT id FROM pg.t WHERE 'fr' = country AND (age > 18 OR country IN ('us', 'uk'))",
		"SELECT id FROM pg.t WHERE country IS NULL",
		"SELECT id FROM pg.t WHERE country LIKE 'f%'",
		"SELECT id FROM pg.a JOIN pg.b ON a.id = b.id AND country = 'fr'",
		"SELECT count(*) FILTER (WHERE country = 'fr') FROM pg.t",
		"SELECT id FROM (SELECT id FROM pg.t WHERE country = 'fr') s",
		"SELECT (SELECT max(id) FROM pg.t WHERE country = 'fr')",
		"SELECT country, lower(country), amount * 2 FROM pg.t",
		"SELECT id FROM pg.t ORDER BY country",
	}
	for _, sql := range read {
		if s := shapeOf(t, sql); slices.Contains(s.UnreadPredicateColumns, "country") {
			t.Errorf("%s: UnreadPredicateColumns %v holds country", sql, s.UnreadPredicateColumns)
		}
	}
	// A quoted name is compared in lower case.
	s := shapeOf(t, `SELECT id FROM pg.t WHERE "Country" = 'de'`)
	if !hasComparison(s.Comparisons, parser.Comparison{Column: "country", Op: "=", Value: "de"}) {
		t.Errorf("quoted column not folded: %+v", s.Comparisons)
	}
}
