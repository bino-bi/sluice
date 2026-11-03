// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestExpressionProbe (plan R18) checks the DuckDB features the expression
// check relies on, in a hardened builder instance. A DuckDB upgrade that
// drops one of them fails here and not in a user build.
func TestExpressionProbe(t *testing.T) {
	ctx := context.Background()
	b := &builder{
		in: Input{
			Source: &apitypes.DataSource{
				Metadata: apitypes.ObjectMeta{Name: "crm"},
				Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: fixturePath(t, false)},
			},
			Secrets: mapSecrets{},
			Harden:  executor.HardenConfig{Threads: 1},
		},
		spec: baseSpec(),
		log:  slog.New(slog.DiscardHandler),
	}
	conn, closeAll, err := b.open(ctx, filepath.Join(t.TempDir(), "probe.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer closeAll()

	parse := func(sql string) map[string]any {
		t.Helper()
		var out string
		if err := conn.QueryRowContext(ctx, "SELECT CAST(json_serialize_sql(CAST(? AS VARCHAR)) AS VARCHAR)", "SELECT ("+sql+")").Scan(&out); err != nil {
			t.Fatalf("json_serialize_sql(%q): %v", sql, err)
		}
		var tree map[string]any
		if err := json.Unmarshal([]byte(out), &tree); err != nil {
			t.Fatalf("json_serialize_sql(%q): %v", sql, err)
		}
		return tree
	}
	// node returns the select node of a parsed tree.
	node := func(tree map[string]any) map[string]any {
		t.Helper()
		if tree["error"] != false {
			t.Fatalf("error = %v, want false: %v", tree["error"], tree)
		}
		stmts, _ := tree["statements"].([]any)
		if len(stmts) != 1 {
			t.Fatalf("statements = %v", tree["statements"])
		}
		n, _ := stmts[0].(map[string]any)["node"].(map[string]any)
		if n["type"] != "SELECT_NODE" {
			t.Fatalf("node type = %v", n["type"])
		}
		for _, k := range []string{"where_clause", "having", "qualify", "group_expressions", "modifiers", "cte_map"} {
			if _, ok := n[k]; !ok {
				t.Errorf("select node has no %s", k)
			}
		}
		return n
	}
	item := func(sql string) map[string]any {
		t.Helper()
		n := node(parse(sql))
		if from, _ := n["from_table"].(map[string]any); from["type"] != "EMPTY" {
			t.Errorf("%q: from_table = %v, want EMPTY", sql, n["from_table"])
		}
		list, _ := n["select_list"].([]any)
		if len(list) != 1 {
			t.Fatalf("%q: select_list has %d items", sql, len(list))
		}
		return list[0].(map[string]any)
	}

	if e := item("quantity * unit_price"); e["class"] != "FUNCTION" || e["is_operator"] != true {
		t.Errorf("operator: class = %v, is_operator = %v", e["class"], e["is_operator"])
	}
	if e := item("quantity"); e["class"] != "COLUMN_REF" || len(e["column_names"].([]any)) != 1 {
		t.Errorf("column: %v", e)
	}
	e := item("abs(quantity)")
	if e["class"] != "FUNCTION" || e["function_name"] != "abs" {
		t.Errorf("function: %v", e)
	}
	for _, k := range []string{"schema", "catalog", "distinct", "filter", "order_bys"} {
		if _, ok := e[k]; !ok {
			t.Errorf("function node has no %s", k)
		}
	}
	if e := item("(SELECT max(a) FROM t)"); e["class"] != "SUBQUERY" {
		t.Errorf("subquery: class = %v", e["class"])
	}
	if n := len(node(parse("a), (b"))["select_list"].([]any)); n != 2 {
		t.Errorf("unbalanced paren: %d select items, want 2", n)
	}
	if tree := parse("quantity +"); tree["error"] != true {
		t.Errorf("syntax error: error = %v, want true", tree["error"])
	}

	for name, want := range map[string][2]string{
		"abs":          {"scalar", "CONSISTENT"},
		"+":            {"scalar", "CONSISTENT"},
		"random":       {"scalar", "VOLATILE"},
		"now":          {"scalar", "CONSISTENT_WITHIN_QUERY"},
		"sum":          {"aggregate", "CONSISTENT"},
		"read_csv":     {"table", ""},
		"current_date": {"", "CONSISTENT_WITHIN_QUERY"},
	} {
		// Every row of the name must agree, so the distinct values are joined.
		var n int
		var typ, stab string
		err := conn.QueryRowContext(ctx, `SELECT count(*),
  coalesce(string_agg(DISTINCT function_type, ','), ''),
  coalesce(string_agg(DISTINCT coalesce(stability, 'NULL'), ','), '')
FROM duckdb_functions() WHERE lower(function_name) = lower(?)`, name).Scan(&n, &typ, &stab)
		if err != nil {
			t.Fatalf("duckdb_functions: %v", err)
		}
		if n == 0 {
			t.Errorf("%s: no row in duckdb_functions()", name)
		}
		if want[0] != "" && typ != want[0] {
			t.Errorf("%s: function_type = %s, want %s", name, typ, want[0])
		}
		if want[1] != "" && stab != want[1] {
			t.Errorf("%s: stability = %s, want %s", name, stab, want[1])
		}
	}
}
