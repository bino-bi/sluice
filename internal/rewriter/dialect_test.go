// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"sync"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/executor"
)

var dialectParseCheck = sync.OnceValues(func() (*executor.Executor, error) {
	return executor.NewParseCheck(context.Background(), 0, nil)
})

// Verdicts of the DuckDB parse check on (sql, want).
const (
	duckSame     = "same"     // DuckDB reads want like sql
	duckDiffers  = "differs"  // the check refuses
	duckRawFails = "rawfails" // DuckDB cannot parse sql
)

func TestNormalizeForDuckDB(t *testing.T) {
	cases := []struct {
		name, sql, want, duck string
	}{
		{"like escape", `SELECT x LIKE 'a!_b' ESCAPE '!' FROM t`, `SELECT like_escape(x, 'a!_b', '!') FROM t`, duckSame},
		{"not like escape", `SELECT x NOT LIKE 'a' ESCAPE '!' FROM t`, `SELECT not_like_escape(x, 'a', '!') FROM t`, duckSame},
		{"ilike escape", `SELECT x ILIKE 'a' ESCAPE '!' FROM t`, `SELECT ilike_escape(x, 'a', '!') FROM t`, duckSame},
		{"not ilike escape", `SELECT x NOT ILIKE 'a' ESCAPE '!' FROM t`, `SELECT not_ilike_escape(x, 'a', '!') FROM t`, duckSame},
		{"like escape under is null", `SELECT id FROM t WHERE (x LIKE 'a' ESCAPE '!') IS NULL`, `SELECT id FROM t WHERE like_escape(x, 'a', '!') IS NULL`, duckSame},
		{"like escape in a nested select", `SELECT (SELECT max(y) FROM u WHERE y LIKE x ESCAPE '#') FROM t`, `SELECT (SELECT max(y) FROM u WHERE like_escape(y, x, '#')) FROM t`, duckSame},
		{"like without escape", `SELECT x LIKE 'a%', x NOT ILIKE 'b%' FROM t`, `SELECT x LIKE 'a%', x NOT ILIKE 'b%' FROM t`, duckSame},
		{"explicit like_escape call", `SELECT x LIKE pg_catalog.like_escape('a', '!') FROM t`, `SELECT x LIKE pg_catalog.like_escape('a', '!') FROM t`, ""},
		{"similar to escape", `SELECT x SIMILAR TO 'a' ESCAPE '!' FROM t`, `SELECT x SIMILAR TO 'a' ESCAPE '!' FROM t`, duckRawFails},

		{"interval day", `SELECT INTERVAL '400' DAY`, `SELECT to_days(trunc('400'::double precision)::int)`, duckSame},
		{"interval year", `SELECT interval '-2' year`, `SELECT to_years(trunc('-2'::double precision)::int)`, duckSame},
		{"interval month", `SELECT INTERVAL '1.5' MONTH`, `SELECT to_months(trunc('1.5'::double precision)::int)`, duckSame},
		{"interval hour", `SELECT INTERVAL '3' HOUR`, `SELECT to_hours(trunc('3'::double precision)::bigint)`, duckSame},
		{"interval minute", `SELECT INTERVAL ' 3 ' MINUTE`, `SELECT to_minutes(trunc(' 3 '::double precision)::bigint)`, duckSame},
		{"interval second", `SELECT INTERVAL '1e2' SECOND`, `SELECT to_seconds('1e2'::double precision)`, duckSame},
		{"interval in where", `SELECT id FROM t WHERE d > now() - INTERVAL '30' DAY`, `SELECT id FROM t WHERE d > (now() - to_days(trunc('30'::double precision)::int))`, duckSame},
		{"interval in a window frame",
			`SELECT count(*) OVER (ORDER BY d RANGE BETWEEN INTERVAL '30' DAY PRECEDING AND CURRENT ROW) FROM t`,
			`SELECT count(*) OVER (ORDER BY d RANGE BETWEEN to_days(trunc('30'::double precision)::int) PRECEDING AND CURRENT ROW) FROM t`, duckSame},
		{"interval literal in an array", `SELECT ARRAY[INTERVAL '1' HOUR]`, `SELECT ARRAY[to_hours(trunc('1'::double precision)::bigint)]`, duckSame},
		{"explicit interval casts", `SELECT '1'::interval day, CAST('1' AS interval hour)`, `SELECT '1'::interval day, '1'::interval hour`, duckSame},
		{"explicit cast in an array", `SELECT ARRAY['1'::interval day]`, `SELECT ARRAY['1'::interval day]`, duckSame},
		{"interval without unit", `SELECT INTERVAL '1 day'`, `SELECT '1 day'::interval`, duckSame},
		{"interval range", `SELECT INTERVAL '1' DAY TO HOUR`, `SELECT '1'::interval day to hour`, duckRawFails},
		{"interval precision", `SELECT INTERVAL '1' SECOND(3)`, `SELECT '1'::interval second(3)`, duckRawFails},
		// pg_query reads WEEK as an alias.
		{"interval week", `SELECT INTERVAL '1' WEEK`, `SELECT '1'::interval AS "WEEK"`, duckDiffers},

		{"json cast", `SELECT '1'::json`, `SELECT '1'::"json"`, duckSame},
		{"json literal", `SELECT JSON '{"a":1}'`, `SELECT '{"a":1}'::"json"`, duckSame},
		{"json array", `SELECT '1'::json[]`, `SELECT '1'::"json"[]`, duckSame},
		{"bit cast", `SELECT CAST(1 AS bit)`, `SELECT 1::"bit"(1)`, duckSame},
		{"bit literal", `SELECT BIT '101'`, `SELECT '101'::"bit"`, duckSame},
		{"bit varying", `SELECT '1'::bit varying, '1'::bit varying(3)`, `SELECT '1'::varbit, '1'::varbit(3)`, duckSame},
		{"other pg_catalog type", `SELECT '12'::pg_catalog.int4`, `SELECT '12'::int`, ""},

		{"extract unit spelling", `SELECT EXTRACT(EPOCH FROM ts), EXTRACT(IsoDow FROM d) FROM t`, `SELECT extract ('EPOCH' FROM ts), extract ('IsoDow' FROM d) FROM t`, duckSame},
		{"extract keyword unit", `SELECT EXTRACT(YEAR FROM d), EXTRACT(Week FROM d) FROM t`, `SELECT extract ('year' FROM d), extract ('week' FROM d) FROM t`, duckSame},
		{"extract plural keyword unit", `SELECT EXTRACT(Days FROM d) FROM t`, `SELECT extract ('days' FROM d) FROM t`, duckDiffers},
		{"extract quoted unit", `SELECT EXTRACT("Epoch" FROM ts), EXTRACT('EPOCH' FROM ts) FROM t`, `SELECT extract ('Epoch' FROM ts), extract ('EPOCH' FROM ts) FROM t`, duckSame},

		{"bare float cast", `SELECT x::float FROM t`, `SELECT x::real FROM t`, duckSame},
		{"bare float in CAST", `SELECT CAST(x AS FLOAT) FROM t`, `SELECT x::real FROM t`, duckSame},
		{"bare float array", `SELECT x::Float[] FROM t`, `SELECT x::real[] FROM t`, duckSame},
		{"float with precision", `SELECT x::float(53), x::float (53) FROM t`, `SELECT x::double precision, x::double precision FROM t`, duckSame},
		{"float with small precision", `SELECT x::float(10) FROM t`, `SELECT x::real FROM t`, duckSame},
		{"float8 and double precision", `SELECT x::float8, x::double precision FROM t`, `SELECT x::float8, x::double precision FROM t`, duckSame},

		{"alias", `SELECT id AS MyId FROM t`, `SELECT id AS "MyId" FROM t`, duckSame},
		{"bare alias with a reference", `SELECT id MyId FROM t ORDER BY MyId`, `SELECT id AS "MyId" FROM t ORDER BY myid`, duckSame},
		{"keyword alias", `SELECT 1 AS Value`, `SELECT 1 AS "Value"`, duckSame},
		{"non-ASCII alias", `SELECT 1 AS ÄBc`, `SELECT 1 AS "ÄBc"`, duckSame},
		{"table and subquery aliases",
			`SELECT to_json(Sub) FROM (SELECT id AS MyId FROM t AS T1) AS Sub`,
			`SELECT to_json(sub) FROM (SELECT id AS "MyId" FROM t "T1") "Sub"`, duckSame},
		{"named argument", `SELECT struct_pack(MyKey := id, Value => 2) FROM t`, `SELECT struct_pack(MyKey := id, Value := 2) FROM t`, duckSame},
		{"cte column list", `WITH c(MyCol) AS (SELECT 1) SELECT * FROM c`, `WITH c("MyCol") AS (SELECT 1) SELECT * FROM c`, duckSame},
		{"values column list", `SELECT * FROM (VALUES (1)) v(MyCol)`, `SELECT * FROM (VALUES (1)) v("MyCol")`, duckSame},
		{"quoted alias", `SELECT 1 AS "MyId"`, `SELECT 1 AS "MyId"`, duckSame},
		// Column, function and table names are references, not spellings.
		{"alias of a column with the same name", `SELECT name AS Name, count(*) AS Count FROM t GROUP BY name`, `SELECT name AS "Name", count(*) AS "Count" FROM t GROUP BY name`, duckSame},
		{"alias used in another case", `SELECT id AS MyId FROM t WHERE myid > 0 ORDER BY MYID`, `SELECT id AS "MyId" FROM t WHERE myid > 0 ORDER BY myid`, duckSame},
		{"qualified references", `SELECT T1.Id AS Id FROM main.T AS T1, pg_catalog.Upper(x) u`, `SELECT t1.id AS "Id" FROM main.t "T1", pg_catalog.upper(x) u`, ""},
		{"table renamed by an alias", `SELECT orders.id FROM Orders AS orders`, `SELECT orders.id FROM orders orders`, duckSame},
		{"names that are not aliases", `SELECT Orders.ID FROM Orders`, `SELECT orders.id FROM orders`, duckSame},
		{"definition in lower case", `SELECT MyId FROM (SELECT id AS myid FROM t) s`, `SELECT myid FROM (SELECT id AS myid FROM t) s`, duckSame},
		{"quoted lower-case definition", `SELECT MyId FROM (SELECT id AS "myid" FROM t) s`, `SELECT myid FROM (SELECT id AS myid FROM t) s`, duckSame},
		{"ambiguous: two definitions", `SELECT 1 AS MyX, 2 AS MYX`, `SELECT 1 AS myx, 2 AS myx`, duckSame},
		{"ambiguous: two scopes", `SELECT x AS MyX FROM (SELECT 2 AS myx) s`, `SELECT x AS myx FROM (SELECT 2 AS myx) s`, duckSame},
		{"ambiguous: a name with no location", `SELECT a AS Col FROM t JOIN u USING (col)`, `SELECT a AS col FROM t JOIN u USING (col)`, duckSame},
		{"unicode escape name", `SELECT 1 AS MyId, 2 AS U&"x"`, `SELECT 1 AS myid, 2 AS x`, ""},
		// A known limitation: a keyword or type name spelled another way
		// makes the alias ambiguous. A quoted alias keeps its spelling.
		{"ambiguous: a keyword", `SELECT EXTRACT(MONTH FROM d) AS Month, d::date AS Date FROM t`, `SELECT extract ('month' FROM d) AS month, d::date AS date FROM t`, duckSame},
		{"quoted alias next to a keyword", `SELECT EXTRACT(MONTH FROM d) AS "Month" FROM t`, `SELECT extract ('month' FROM d) AS "Month" FROM t`, duckSame},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, err := pg.Parse(tc.sql)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			normalizeForDuckDB(tree, tc.sql)
			got, err := pg.Deparse(tree)
			if err != nil {
				t.Fatalf("deparse: %v", err)
			}
			if got != tc.want {
				t.Errorf("normalized deparse\n got: %s\nwant: %s", got, tc.want)
			}
			if tc.duck == "" {
				return
			}
			exec, err := dialectParseCheck()
			if err != nil {
				t.Fatalf("parse check executor: %v", err)
			}
			rawTree, depTree, err := exec.ParseTrees(context.Background(), tc.sql, got)
			if err != nil {
				t.Fatalf("ParseTrees: %v", err)
			}
			same, rawParses := sameDuckDBParse(rawTree, depTree)
			verdict := duckDiffers
			switch {
			case !rawParses:
				verdict = duckRawFails
			case same:
				verdict = duckSame
			}
			if verdict != tc.duck {
				t.Errorf("DuckDB verdict %s, want %s\n raw: %s\n dep: %s", verdict, tc.duck, rawTree, depTree)
			}
		})
	}
}

func TestSameDuckDBParse(t *testing.T) {
	const esc = `\` + "u" // a JSON unicode escape
	cases := []struct {
		name            string
		raw, dep        string
		same, rawParses bool
	}{
		{"identical", `{"error":false,"a":[1,2]}`, `{"error":false,"a":[1,2]}`, true, true},
		{"structure differs",
			`{"error":false,"type":"OPERATOR_IS_NULL"}`,
			`{"error":false,"type":"CONJUNCTION_OR"}`, false, true},
		{"longer side differs", `{"error":false}`, `{"error":false}x`, false, true},
		{"raw error", `{"error":true,"error_type":"parser"}`, `{"error":false}`, false, false},
		{"both error", `{"error":true,"x":1}`, `{"error":true,"x":1}`, false, false},
		{"deparsed error", `{"error":false}`, `{"error":true,"error_type":"parser"}`, false, true},

		{"location differs",
			`{"a":1,"query_location":7,"b":2}`,
			`{"a":1,"query_location":12,"b":2}`, true, true},
		{"location on one side",
			`{"query_location":7,"x":1}`,
			`{"x":1}`, true, true},
		{"location last key",
			`{"x":1,"query_location":18446744073709551615}`,
			`{"x":1,"query_location":3}`, true, true},
		{"location text in a constant",
			`{"value":"\"query_location\":5"}`,
			`{"value":"\"query_location\":6"}`, false, true},
		{"location text in an alias",
			`{"alias":"\"query_location\":1"}`,
			`{"alias":"\"query_location\":2"}`, false, true},

		{"column name case", `{"column_names":["ID"]}`, `{"column_names":["id"]}`, true, true},
		{"table name case", `{"table_name":"T"}`, `{"table_name":"t"}`, true, true},
		{"function name case", `{"function_name":"Upper"}`, `{"function_name":"upper"}`, true, true},
		{"alias case", `{"alias":"MyId"}`, `{"alias":"myid"}`, true, true},
		{"aliases case", `{"aliases":["MyCol"]}`, `{"aliases":["mycol"]}`, true, true},
		{"column_name_alias case", `{"column_name_alias":["A","b"]}`, `{"column_name_alias":["a","B"]}`, true, true},
		{"alias differs", `{"alias":"x"}`, `{"alias":"y"}`, false, true},
		{"Kelvin sign is not k", "{\"alias\":\"K\"}", `{"alias":"k"}`, false, true},
		{"long s is not s", "{\"alias\":\"ſ\"}", `{"alias":"s"}`, false, true},
		{"multibyte kept", `{"alias":"列"}`, `{"alias":"列"}`, true, true},

		{"constant case kept", `{"value":"ABC"}`, `{"value":"abc"}`, false, true},
		{"constant equal", `{"value":"ABC"}`, `{"value":"ABC"}`, true, true},
		{"constant escaped quote", `{"value":"a\"B"}`, `{"value":"a\"b"}`, false, true},
		{"constant escaped quote equal", `{"value":"a\"B\\"}`, `{"value":"a\"B\\"}`, true, true},
		{"constant escape case", `{"value":"` + esc + `00E9"}`, `{"value":"` + esc + `00e9"}`, false, true},
		{"name escape case", `{"alias":"` + esc + `00E9"}`, `{"alias":"` + esc + `00e9"}`, true, true},
		{"constant number", `{"value":1}`, `{"value":2}`, false, true},
		{"constant nested",
			`{"value":{"type":{"id":"VARCHAR"},"is_null":false,"value":"X"}}`,
			`{"value":{"type":{"id":"varchar"},"is_null":false,"value":"x"}}`, false, true},
		{"constant nested equal",
			`{"value":{"type":{"id":"VARCHAR"},"is_null":false,"value":"X"}}`,
			`{"value":{"type":{"id":"varchar"},"is_null":false,"value":"X"}}`, true, true},
		{"constant against object", `{"value":"x"}`, `{"value":{"x":1}}`, false, true},
		{"value key text inside an alias", `{"alias":"\"value\":\"A"}`, `{"alias":"\"value\":\"a"}`, true, true},
		{"value as a string, not a key", `{"aliases":["value","A"]}`, `{"aliases":["value","a"]}`, true, true},

		{"main schema", `{"function_name":"like_escape","schema":"main"}`, `{"function_name":"like_escape","schema":""}`, true, true},
		{"main schema both", `{"schema":"main"}`, `{"schema":"main"}`, true, true},
		{"main schema on the deparsed side", `{"schema":""}`, `{"schema":"main"}`, true, true},
		{"other schema", `{"schema":"pg_catalog"}`, `{"schema":""}`, false, true},
		{"main is not a table schema fold", `{"schema_name":"main"}`, `{"schema_name":""}`, false, true},

		{"unterminated string", `{"alias":"ab`, `{"alias":"ab`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			same, rawParses := sameDuckDBParse(tc.raw, tc.dep)
			if same != tc.same || rawParses != tc.rawParses {
				t.Errorf("sameDuckDBParse(%s, %s) = (%v, %v), want (%v, %v)",
					tc.raw, tc.dep, same, rawParses, tc.same, tc.rawParses)
			}
		})
	}
}
