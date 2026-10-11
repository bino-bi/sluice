// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"strings"
	"testing"
)

func TestCheckFunctions(t *testing.T) {
	cases := []struct {
		name  string
		shape QueryShape
		want  string // substring of the reason; "" means allowed
	}{
		{"no functions", QueryShape{}, ""},
		{"allowlisted expression functions", QueryShape{Functions: []FuncRef{
			{Name: "upper"}, {Name: "count"}, {Name: "row_number"}, {Name: "date_trunc"},
		}}, ""},
		{"SQL syntax forms", QueryShape{Functions: []FuncRef{
			{Name: "extract"}, {Name: "btrim"}, {Name: "like_escape", Qualifier: "pg_catalog"},
			{Name: "similar_to_escape", Qualifier: "pg_catalog"},
			{Name: "current_timestamp"}, {Name: "ifnull"}, {Name: "date"}, {Name: "try"}, {Name: "grouping_id"},
		}}, ""},
		{"allowlisted table functions", QueryShape{Functions: []FuncRef{
			{Name: "generate_series", InFrom: true}, {Name: "range", InFrom: true}, {Name: "unnest", InFrom: true},
		}}, ""},
		{"unnest in the select list", QueryShape{Functions: []FuncRef{{Name: "unnest"}}}, ""},

		{"table function", QueryShape{Functions: []FuncRef{{Name: "query_table", InFrom: true}}}, `table function "query_table"`},
		{"scalar function used in FROM", QueryShape{Functions: []FuncRef{{Name: "upper", InFrom: true}}}, `table function "upper"`},
		{"table function with dynamic arguments", QueryShape{Functions: []FuncRef{
			{Name: "unnest", InFrom: true, DynamicArgs: true},
		}}, "must not contain columns or subqueries"},
		{"state reader", QueryShape{Functions: []FuncRef{{Name: "current_setting"}}}, `function "current_setting"`},
		{"star expression", QueryShape{Functions: []FuncRef{{Name: "columns"}}}, `function "columns"`},
		{"session keyword", QueryShape{Functions: []FuncRef{{Name: "current_user"}}}, `function "current_user"`},
		{"table function name in an expression", QueryShape{Functions: []FuncRef{{Name: "read_csv"}}}, `function "read_csv"`},
		{"unknown function", QueryShape{Functions: []FuncRef{{Name: "added_in_a_later_release"}}}, "is not allowed"},
		{"empty name", QueryShape{Functions: []FuncRef{{}}}, "is not allowed"},
		{"qualified allowlisted function", QueryShape{Functions: []FuncRef{
			{Name: "upper", Qualifier: "main"},
		}}, `qualified function name "main.upper"`},
		{"method call syntax", QueryShape{Functions: []FuncRef{
			{Name: "upper", Qualifier: "email"},
		}}, `qualified function name "email.upper"`},
		{"pg_catalog qualifier written by the caller", QueryShape{Functions: []FuncRef{
			{Name: "upper", Qualifier: "pg_catalog"},
		}}, `qualified function name "pg_catalog.upper"`},
		{"qualified table function", QueryShape{Functions: []FuncRef{
			{Name: "range", Qualifier: "shop.main", InFrom: true},
		}}, "qualified function name"},
		{"pg_catalog does not unlock other names", QueryShape{Functions: []FuncRef{
			{Name: "pg_get_viewdef", Qualifier: "pg_catalog"},
		}}, `qualified function name "pg_catalog.pg_get_viewdef"`},
		{"unsupported FROM item", QueryShape{UnsupportedFrom: []string{"TABLESAMPLE"}}, "TABLESAMPLE in FROM is not supported"},
		{"one bad call among good ones", QueryShape{Functions: []FuncRef{
			{Name: "upper"}, {Name: "getvariable"}, {Name: "lower"},
		}}, `function "getvariable"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckFunctions(tc.shape)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("rejected with %q, want allowed", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("reason = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// The lists are matched against lowercased names.
func TestFunctionAllowlistsAreLowercase(t *testing.T) {
	for _, set := range []map[string]struct{}{tableFunctions, syntaxFunctions, exprFunctions} {
		for name := range set {
			if name != strings.ToLower(name) {
				t.Errorf("%q must be lowercase", name)
			}
		}
	}
}

// Functions that read state outside their arguments must stay off the
// lists, in FROM and in expressions.
func TestStateReadersAreNotAllowlisted(t *testing.T) {
	names := []string{
		"current_setting", "getvariable", "getenv", "nextval", "currval",
		"current_database", "current_schema", "current_schemas", "current_query",
		"current_catalog", "current_user", "current_role", "session_user", "user",
		"version", "in_search_path", "get_block_size", "stats", "columns",
		"typeof", "enum_range", "enum_range_boundary", "enum_first", "enum_last",
		"list_aggregate", "list_aggr", "array_aggregate", "array_aggr", "aggregate",
		"json_serialize", "json_query", "xmlelement", "merge_action",
		"json_serialize_sql", "json_deserialize_sql", "json_serialize_plan",
		"pg_get_viewdef", "pg_get_expr", "has_table_privilege", "obj_description",
		"error", "sleep_ms", "setseed", "write_log",
		"query", "query_table", "read_csv", "read_parquet", "read_text", "read_blob",
		"glob", "sqlite_scan", "duckdb_settings", "duckdb_databases", "duckdb_secrets",
		"which_secret", "pragma_database_size",
	}
	for _, name := range names {
		for _, inFrom := range []bool{false, true} {
			shape := QueryShape{Functions: []FuncRef{{Name: name, InFrom: inFrom}}}
			if CheckFunctions(shape) == "" {
				t.Errorf("%q (inFrom=%v) passes the function gate", name, inFrom)
			}
		}
	}
}

// The reason goes to the client and into the audit record. A qualifier can
// have any number of parts, so it must not set the size of either.
func TestCheckFunctionsBoundsTheReason(t *testing.T) {
	shape := QueryShape{Functions: []FuncRef{{Name: "f", Qualifier: strings.Repeat("a.", 100000) + "a"}}}
	if got := CheckFunctions(shape); len(got) > 256 {
		t.Fatalf("reason has %d bytes", len(got))
	}
}
