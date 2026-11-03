// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// R10.
func TestExpressionColumns(t *testing.T) {
	spec := ordersSpec(map[string]apitypes.SyntheticColumn{
		// The source total has 10 % NULLs; without nullRate the expression has none.
		"total":  {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "quantity * unit_price"}},
		"status": {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "'q' || CAST(Quantity AS VARCHAR)", "nullRate": 0.3}},
	})
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: spec, dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	if n := scalar[int64](t, db, "SELECT count(*) FROM src.crm.orders WHERE total IS NULL"); n == 0 {
		t.Fatal("the source total has no NULL")
	}
	for _, q := range []string{
		"SELECT count(*) FROM syn.crm.orders WHERE total IS DISTINCT FROM CAST(quantity * unit_price AS DECIMAL(12,2))",
		"SELECT count(*) FROM syn.crm.orders WHERE total IS NULL",
		"SELECT count(*) FROM syn.crm.orders WHERE status IS NOT NULL AND status <> 'q' || quantity",
	} {
		if n := scalar[int64](t, db, q); n != 0 {
			t.Errorf("%d rows: %s", n, q)
		}
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders WHERE status IS NULL"); n == 0 {
		t.Error("no NULL status, nullRate 0.3 shows nothing")
	}
	if typ := scalar[string](t, db, "SELECT data_type FROM information_schema.columns WHERE table_catalog = 'syn' AND table_name = 'orders' AND column_name = 'total'"); typ != "DECIMAL(12,2)" {
		t.Errorf("total type = %s", typ)
	}
	want := map[string][]string{"total": {"quantity", "unit_price"}, "status": {"quantity"}}
	for _, c := range rep.Tables[0].Columns {
		if w, ok := want[c.Name]; ok && (!slices.Equal(c.DependsOn, w) || c.Generator != "expression" || c.Distance != nil) {
			t.Errorf("column %s report: depends_on %v, generator %s, distance %+v", c.Name, c.DependsOn, c.Generator, c.Distance)
		}
	}

	// The content hash covers the computed values: the staged rows are the
	// same, only the expression differs.
	spec.Tables[0].Columns["total"] = apitypes.SyntheticColumn{Generator: apitypes.GenExpression, Args: map[string]any{"sql": "quantity * unit_price + 1"}}
	other, _ := mustRun(t, opts{spec: spec, source: relationsPath(t)})
	if other.ContentHash == rep.ContentHash {
		t.Error("content hash does not change with the expression")
	}
}

// The check runs at build start: the first table would fail its size
// check, but the expression error of the second table comes first.
func TestExpressionCheckedFirst(t *testing.T) {
	spec := relSpec(nil)
	spec.Tables[0].Size = apitypes.SyntheticSize{Rows: i64(4000)}
	spec.Tables = append(spec.Tables, apitypes.SyntheticTable{
		Name: "crm.orders",
		Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
		Columns: map[string]apitypes.SyntheticColumn{
			"total":    {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "(SELECT max(quantity) FROM crm.orders)"}},
			"quantity": {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "random()"}},
		},
	})
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	want := "table crm.orders: column quantity: expression calls function random, which is not allowed\n" +
		"column total: expression uses a subquery, which is not allowed"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestExpressionCastError(t *testing.T) {
	spec := ordersSpec(map[string]apitypes.SyntheticColumn{
		"total": {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "CAST(status AS INTEGER)"}},
	})
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	want := "table crm.orders: column total: the expression fails or does not cast to DECIMAL(12,2)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestCheckExpression(t *testing.T) {
	ctx := context.Background()
	b := &builder{
		in: Input{
			Source: &apitypes.DataSource{
				Metadata: apitypes.ObjectMeta{Name: "crm"},
				Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: relationsPath(t)},
			},
			Secrets: mapSecrets{},
			Harden:  executor.HardenConfig{Threads: 1},
		},
		spec: relSpec(nil),
		log:  slog.New(slog.DiscardHandler),
	}
	conn, closeAll, err := b.open(ctx, filepath.Join(t.TempDir(), "check.duckdb"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer closeAll()
	cols := map[string]string{"quantity": "quantity", "unit_price": "unit_price", "status": "status"}
	exprCols := map[string]bool{"total": true, "other": true}

	exprFunctions["sluice_no_such_fn"] = true
	defer delete(exprFunctions, "sluice_no_such_fn")

	for _, tc := range []struct{ sql, want string }{
		{"quantity -- x", "holds a comment, a line break or ;"},
		{"quantity /* x */", "holds a comment, a line break or ;"},
		{"quantity\n+ 1", "holds a comment, a line break or ;"},
		{"quantity\r+ 1", "holds a comment, a line break or ;"},
		{"quantity), (unit_price", "is not a single expression"},
		{"quantity +", "does not parse"},
		{"sluice_no_such_fn(quantity)", "calls function sluice_no_such_fn, which is not allowed"},
		{"RANDOM()", "calls function random, which is not allowed"},
		{"now()", "calls function now, which is not allowed"},
		{"current_localtimestamp()", "calls function current_localtimestamp, which is not allowed"},
		{"current_localtime()", "calls function current_localtime, which is not allowed"},
		{"age(TIMESTAMP '2020-01-01')", "calls function age, which is not allowed"},
		{"age(TIMESTAMP '2020-01-01', TIMESTAMP '2021-01-01')", "calls function age, which is not allowed"},
		{"sum(quantity)", "calls function sum, which is not allowed"},
		{"read_csv('x')", "calls function read_csv, which is not allowed"},
		{"current_setting('threads')", "calls function current_setting, which is not allowed"},
		{"main.abs(quantity)", "calls function abs with a schema, which is not allowed"},
		{"count(DISTINCT quantity)", "calls function count with DISTINCT, FILTER or ORDER BY, which is not allowed"},
		{"(SELECT max(quantity) FROM crm.orders)", "uses a subquery, which is not allowed"},
		{"quantity IN (SELECT 1)", "uses a subquery, which is not allowed"},
		{"row_number() OVER ()", "uses a window function, which is not allowed"},
		{"list_transform(quantity, x -> x)", "uses a lambda, which is not allowed"},
		{"[quantity]", "calls function list_value with a schema, which is not allowed"},
		{"status COLLATE nocase", "uses COLLATE, which is not allowed"},
		{"CAST(status AS crm.g)", "casts to a named type, which is not allowed"},
		{"o.quantity", "names column o.quantity, which is not a column of the table"},
		{"nope + 1", "names column nope, which is not a column of the table"},
		{"quantity + Other", "names column Other, which is an expression column"},
		{"total", "names column total, which is an expression column"},
	} {
		refs, err := b.checkExpression(ctx, conn, tc.sql, cols, exprCols)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%q: refs %v, err = %v, want %q", tc.sql, refs, err, tc.want)
		}
	}

	for sql, want := range map[string][]string{
		"quantity * unit_price":                                     {"quantity", "unit_price"},
		"CASE WHEN Quantity > 3 THEN 'big' ELSE status END":         {"quantity", "status"},
		"coalesce(abs(-quantity), 0) BETWEEN 1 AND 5 AND NOT false": {"quantity"},
		"status LIKE 'p%' OR status IS NULL":                        {"status"},
		"round(unit_price::DOUBLE, 1)":                              {"unit_price"},
		"42":                                                        {},
	} {
		refs, err := b.checkExpression(ctx, conn, sql, cols, exprCols)
		if err != nil || !slices.Equal(refs, want) {
			t.Errorf("%q: refs %v, err %v, want %v", sql, refs, err, want)
		}
	}
	// No DuckDB text: the parser says "Parser Error" or "syntax error".
	if _, err := b.checkExpression(ctx, conn, "quantity + )", cols, exprCols); err == nil || strings.Contains(strings.ToLower(err.Error()), "error") {
		t.Errorf("syntax error: %v", err)
	}
}
