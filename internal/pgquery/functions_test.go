// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
)

func fn(name string) parser.FuncRef   { return parser.FuncRef{Name: name} }
func from(name string) parser.FuncRef { return parser.FuncRef{Name: name, InFrom: true} }
func pgcat(name string) parser.FuncRef {
	return parser.FuncRef{Name: name, Qualifier: "pg_catalog"}
}

// TestCollectFunctions locks what the function gate sees. A call the
// collector misses is a call policy cannot refuse.
func TestCollectFunctions(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		wantFuncs []parser.FuncRef
		wantFrom  []string
	}{
		{
			name:      "function in FROM",
			sql:       "SELECT t.id FROM query_table('shop.main.customers') t",
			wantFuncs: []parser.FuncRef{from("query_table")},
		},
		{
			name:      "function in FROM next to a table",
			sql:       "SELECT 1 FROM a, duckdb_settings() s",
			wantFuncs: []parser.FuncRef{from("duckdb_settings")},
		},
		{
			name:      "ROWS FROM with ordinality and lateral",
			sql:       "SELECT * FROM ROWS FROM (generate_series(1, 2), range(3)) WITH ORDINALITY r, LATERAL unnest(ARRAY[1]) u",
			wantFuncs: []parser.FuncRef{from("generate_series"), from("range"), from("unnest")},
		},
		{
			name:      "function inside a join",
			sql:       "SELECT 1 FROM a JOIN (b LEFT JOIN read_csv('f') c ON true) ON true",
			wantFuncs: []parser.FuncRef{from("read_csv")},
		},
		{
			name:      "set operation arm, values list and subquery",
			sql:       "SELECT * FROM a UNION ALL SELECT * FROM glob('*') UNION SELECT * FROM (VALUES ((SELECT max(len(s)) FROM sniff_csv('f')))) v",
			wantFuncs: []parser.FuncRef{from("glob"), from("sniff_csv"), fn("len"), fn("max")},
		},
		{
			name: "CTE, sublinks, limit",
			sql: "WITH c AS (SELECT * FROM read_parquet('f')) SELECT (SELECT md5(s) FROM query('x')) FROM c " +
				"WHERE (SELECT lower(s) FROM read_text('f')) IN (SELECT upper(b) FROM d) LIMIT (SELECT count(*) FROM range(3))",
			wantFuncs: []parser.FuncRef{
				from("query"), from("range"), from("read_parquet"), from("read_text"),
				fn("count"), fn("lower"), fn("md5"), fn("upper"),
			},
		},
		{
			name:      "EXPLAIN body",
			sql:       "EXPLAIN ANALYZE SELECT * FROM query_table('x')",
			wantFuncs: []parser.FuncRef{from("query_table")},
		},
		{
			name: "aggregate filter, order, window, within group",
			sql: "SELECT count(*) FILTER (WHERE upper(s) = 'X'), string_agg(s, ',' ORDER BY lower(s)), " +
				"row_number() OVER (PARTITION BY abs(a) ORDER BY ceil(b)), percentile_cont(0.5) WITHIN GROUP (ORDER BY floor(a)) FROM t",
			wantFuncs: []parser.FuncRef{
				fn("abs"), fn("ceil"), fn("count"), fn("floor"), fn("lower"),
				fn("percentile_cont"), fn("row_number"), fn("string_agg"), fn("upper"),
			},
		},
		{
			name: "SQL syntax forms lose the pg_catalog qualifier pg_query adds",
			sql: "SELECT EXTRACT(year FROM d), SUBSTRING(s FROM 1 FOR 2), POSITION('a' IN s), TRIM(s), TRIM(LEADING 'x' FROM s), " +
				"TRIM(TRAILING FROM s), d AT TIME ZONE 'UTC', s LIKE 'a' ESCAPE 'b', s SIMILAR TO 'a' FROM t",
			wantFuncs: []parser.FuncRef{
				fn("btrim"), fn("extract"), pgcat("like_escape"), fn("ltrim"), fn("position"),
				fn("rtrim"), pgcat("similar_to_escape"), fn("substring"), fn("timezone"),
			},
		},
		{
			name:      "pg_catalog qualifier written by the caller stays",
			sql:       "SELECT pg_catalog.lower(s), pg_catalog.substring(s, 1, 2) FROM t",
			wantFuncs: []parser.FuncRef{pgcat("lower"), pgcat("substring")},
		},
		{
			name: "keywords",
			sql:  "SELECT CURRENT_DATE, CURRENT_TIME(3), LOCALTIMESTAMP, CURRENT_USER, SESSION_USER, CURRENT_CATALOG, CURRENT_SCHEMA FROM t",
			wantFuncs: []parser.FuncRef{
				fn("current_catalog"), fn("current_date"), fn("current_schema"), fn("current_time"),
				fn("current_user"), fn("localtimestamp"), fn("session_user"),
			},
		},
		{
			name: "syntax that is not a function call",
			sql:  "SELECT COALESCE(a, 1), GREATEST(a, 2), NULLIF(a, 1), CAST(a AS int), a::text, ROW(a, b), ARRAY[a, b], CASE WHEN a THEN 1 END FROM t",
		},
		{
			name: "qualified and quoted names",
			sql:  `SELECT main.upper(s), x.email.lower(), "MD5"(s) FROM "Query_Table"('x'), shop.main.my_macro() m`,
			wantFuncs: []parser.FuncRef{
				{Name: "my_macro", Qualifier: "shop.main", InFrom: true},
				from("query_table"),
				{Name: "lower", Qualifier: "x.email"},
				fn("md5"),
				{Name: "upper", Qualifier: "main"},
			},
		},
		{
			name: "FROM function arguments",
			sql:  "SELECT 1 FROM t, unnest(t.a) u, generate_series(1, (SELECT 2)) g, range(abs(-3)) r",
			wantFuncs: []parser.FuncRef{
				{Name: "generate_series", InFrom: true, DynamicArgs: true},
				from("range"),
				{Name: "unnest", InFrom: true, DynamicArgs: true},
				fn("abs"),
			},
		},
		{
			name: "SQL/JSON syntax nodes are named",
			sql: "SELECT JSON_ARRAY(1, 2), JSON_OBJECT('a': 1), JSON_SCALAR(1), JSON_SERIALIZE('{}'), " +
				"JSON_VALUE('{}', '$.a'), JSON_QUERY('{}', '$.a'), JSON_EXISTS('{}', '$.a') FROM t",
			wantFuncs: []parser.FuncRef{
				fn("json_array"), fn("json_exists"), fn("json_object"), fn("json_query"),
				fn("json_scalar"), fn("json_serialize"), fn("json_value"),
			},
		},
		{
			// A quoted name is a column to pg_query and to the mask pass.
			name:      "only the unquoted session keyword is a call",
			sql:       `SELECT "current_user", "user", t."user", user FROM t`,
			wantFuncs: []parser.FuncRef{fn("user")},
		},
		{
			name:     "URL as table name",
			sql:      `SELECT * FROM a, "s3://bucket/data.parquet" t`,
			wantFrom: []string{`table name with path characters (/ \ : * ? [)`},
		},
		{
			name:     "path split over schema and table",
			sql:      `SELECT * FROM a UNION SELECT * FROM (SELECT * FROM "/data/x".csv) q`,
			wantFrom: []string{`table name with path characters (/ \ : * ? [)`},
		},
		{
			name:     "TABLESAMPLE",
			sql:      "SELECT id FROM t TABLESAMPLE reservoir (1000)",
			wantFrom: []string{"TABLESAMPLE"},
		},
		{
			name:      "TABLESAMPLE inside a join in a set operation arm",
			sql:       "SELECT 1 FROM a UNION SELECT 1 FROM b JOIN c TABLESAMPLE system (10) ON true",
			wantFrom:  []string{"TABLESAMPLE"},
			wantFuncs: nil,
		},
		{
			name:     "XMLTABLE",
			sql:      "SELECT * FROM XMLTABLE('/a' PASSING '<a/>' COLUMNS id int) x",
			wantFrom: []string{"XMLTABLE"},
		},
		{
			name:      "keyword and cast forms in FROM",
			sql:       "SELECT * FROM CURRENT_DATE d, CAST('x' AS text) c",
			wantFuncs: []parser.FuncRef{fn("current_date")},
			wantFrom:  []string{"non-function expression"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ast, err := newTestParser(t).Parse(context.Background(), tc.sql)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			shape := ast.Shape()
			if !reflect.DeepEqual(shape.Functions, tc.wantFuncs) {
				t.Errorf("functions:\n got  %+v\n want %+v", shape.Functions, tc.wantFuncs)
			}
			if !reflect.DeepEqual(shape.UnsupportedFrom, tc.wantFrom) {
				t.Errorf("unsupported FROM: got %v, want %v", shape.UnsupportedFrom, tc.wantFrom)
			}
		})
	}
}
