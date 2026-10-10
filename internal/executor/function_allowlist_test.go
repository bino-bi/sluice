// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"strings"
	"testing"

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

// The join-keyword alias guard must list exactly the DuckDB keywords that
// act as a join-type prefix. If DuckDB adds or renames one, this fails.
func TestJoinKeywordAliasesAreDuckDBKeywords(t *testing.T) {
	e := newExec(t, executor.Config{})
	for _, kw := range []string{"semi", "anti", "asof", "positional"} {
		var n int
		if err := e.DB().QueryRowContext(context.Background(),
			"SELECT count(*) FROM duckdb_keywords() WHERE keyword_name = ?", kw).Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}
		if n == 0 {
			t.Errorf("%q is not a DuckDB keyword; the alias guard may be stale", kw)
		}
	}
}
