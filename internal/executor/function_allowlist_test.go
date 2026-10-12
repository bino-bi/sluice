// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
)

// TestFunctionAllowlistMatchesDuckDB checks the function gate's lists
// against the catalog of the embedded DuckDB. It catches typos, entries
// that a DuckDB upgrade removed, and a listed name that is not the kind
// of function the review assumed.
func TestFunctionAllowlistMatchesDuckDB(t *testing.T) {
	e := newExec(t, executor.Config{})
	rows, err := e.DB().QueryContext(context.Background(), `
SELECT lower(function_name), function_type, coalesce(macro_definition, '')
FROM duckdb_functions()
WHERE database_name = 'system' AND schema_name = 'main'`)
	if err != nil {
		t.Fatalf("duckdb_functions: %v", err)
	}
	defer func() { _ = rows.Close() }()

	types := map[string]map[string]bool{}
	macros := map[string]string{}
	for rows.Next() {
		var name, typ, macro string
		if err := rows.Scan(&name, &typ, &macro); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if types[name] == nil {
			types[name] = map[string]bool{}
		}
		types[name][typ] = true
		if typ == "macro" {
			macros[name] = strings.ToLower(macro)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, name := range parser.AllowedExprFunctions() {
		got := types[name]
		if !got["scalar"] && !got["aggregate"] && !got["macro"] {
			t.Errorf("expression function %q is not a scalar, aggregate or macro in system.main (types: %v)", name, got)
		}
		// A macro may wrap a subquery over a table function; such a macro
		// reads state the gate cannot see.
		if def := macros[name]; strings.Contains(def, "select") || strings.Contains(def, " from ") {
			t.Errorf("macro %q is defined with a subquery: %s", name, def)
		}
	}
	for _, name := range parser.AllowedTableFunctions() {
		if !types[name]["table"] {
			t.Errorf("table function %q is not a table function in system.main (types: %v)", name, types[name])
		}
	}
}

// TestWrapperAliasKeywords checks the unsafe alias list against DuckDB.
// For every word of duckdb_keywords() it deparses a subquery aliased with
// that word and joined to a table, the shape a row-filter wrapper takes,
// and asks DuckDB how it reads the statement. A list entry that DuckDB no
// longer lists is not reported; it only costs an extra refusal. Re-run it
// on every DuckDB or pg_query upgrade.
func TestWrapperAliasKeywords(t *testing.T) {
	ctx := context.Background()
	e := newExec(t, executor.Config{})

	rows, err := e.DB().QueryContext(ctx, "SELECT keyword_name FROM duckdb_keywords()")
	if err != nil {
		t.Fatalf("duckdb_keywords: %v", err)
	}
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			t.Fatalf("scan: %v", err)
		}
		words = append(words, w)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(words) < 100 {
		t.Fatalf("duckdb_keywords returned %d words", len(words))
	}

	var unsafe []string
	for _, w := range words {
		tree, err := pg.Parse("SELECT * FROM (SELECT 1 AS x) placeholder JOIN u ON true")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		join0 := tree.Stmts[0].Stmt.GetSelectStmt().FromClause[0].GetJoinExpr()
		join0.Larg.GetRangeSubselect().Alias.Aliasname = w
		sql, err := pg.Deparse(tree)
		if err != nil {
			t.Fatalf("deparse %q: %v", w, err)
		}

		// json_serialize_sql takes a constant only.
		var out string
		q := "SELECT json_serialize_sql('" + strings.ReplaceAll(sql, "'", "''") + "')::VARCHAR"
		if err := e.DB().QueryRowContext(ctx, q).Scan(&out); err != nil {
			t.Fatalf("json_serialize_sql(%q): %v", sql, err)
		}
		var parsed struct {
			Error      bool `json:"error"`
			Statements []struct {
				Node struct {
					FromTable struct {
						Type string `json:"type"`
						Left struct {
							Alias string `json:"alias"`
						} `json:"left"`
					} `json:"from_table"`
				} `json:"node"`
			} `json:"statements"`
		}
		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			t.Fatalf("decode %s: %v", out, err)
		}
		readsAlias := !parsed.Error && len(parsed.Statements) == 1 &&
			parsed.Statements[0].Node.FromTable.Type == "JOIN" &&
			strings.EqualFold(parsed.Statements[0].Node.FromTable.Left.Alias, w)
		if !readsAlias {
			unsafe = append(unsafe, w)
		}
	}
	slices.Sort(unsafe)

	for _, w := range words {
		if got, want := parser.IsUnsafeBareAlias(w), slices.Contains(unsafe, w); got != want {
			t.Errorf("IsUnsafeBareAlias(%q) = %v, DuckDB says %v", w, got, want)
		}
	}
	if t.Failed() {
		t.Logf("DuckDB does not read these as a bare alias: %s", strings.Join(unsafe, " "))
	}
}
