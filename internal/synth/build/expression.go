// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// exprColumn is an expression column after its check.
type exprColumn struct {
	sql string
	// refs are the source names of the columns it reads, sorted.
	refs []string
}

// exprClasses are the parse tree classes an expression may hold.
var exprClasses = map[string]bool{
	"CONSTANT": true, "COLUMN_REF": true, "FUNCTION": true, "OPERATOR": true, "COMPARISON": true,
	"CONJUNCTION": true, "CASE": true, "CAST": true, "BETWEEN": true,
}

// exprClassText names the refused classes in an error.
var exprClassText = map[string]string{
	"SUBQUERY": "a subquery", "WINDOW": "a window function", "STAR": "*", "PARAMETER": "a parameter",
	"LAMBDA": "a lambda", "COLUMNS": "COLUMNS(...)", "COLLATE": "COLLATE",
}

// exprFunctions are the functions an expression may call, before the
// duckdb_functions() check. A var, so a test can add a name DuckDB does
// not know.
var exprFunctions = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range parser.AllowedExprFunctions() {
		m[strings.ToLower(f)] = true
	}
	return m
}()

// clockFunctions read the wall clock, but DuckDB marks them CONSISTENT, so
// the stability check does not refuse them. age refuses with any argument
// count: with one argument it counts from now.
var clockFunctions = map[string]bool{"age": true, "current_localtime": true, "current_localtimestamp": true}

// reservedName is the alias of a staged expression marker in the write.
var reservedName = regexp.MustCompile(`^sluice_x_[0-9]+$`)

func exprMarkerName(i int) string { return fmt.Sprintf("sluice_x_%d", i) }

// checkExpressions checks the expression columns of every table at build
// start, before any table is profiled (plan 1.7). It reads only the
// column names of the source tables.
func (b *builder) checkExpressions(ctx context.Context, conn *sql.Conn) error {
	b.exprs = map[string]map[string]exprColumn{}
	for i := range b.spec.Tables {
		t := &b.spec.Tables[i]
		exprCols := map[string]bool{}
		for name, c := range t.Columns {
			if c.Generator == apitypes.GenExpression {
				exprCols[strings.ToLower(name)] = true
			}
		}
		if len(exprCols) == 0 {
			continue
		}
		schema, name, _ := strings.Cut(t.Name, ".")
		infos, err := b.sourceColumns(ctx, conn, profile.Table{Catalog: b.spec.Source, Schema: schema, Name: name})
		if err != nil {
			return fmt.Errorf("table %s: %w", t.Name, err)
		}
		// cols are the columns an expression may read, by lower-case name.
		cols := map[string]string{}
		var errs []error
		for _, info := range infos {
			if reservedName.MatchString(strings.ToLower(info.Name)) {
				errs = append(errs, fmt.Errorf("column %s: the name is reserved in a table with expression columns", info.Name))
			}
			if !exprCols[strings.ToLower(info.Name)] {
				cols[strings.ToLower(info.Name)] = info.Name
			}
		}
		checked := map[string]exprColumn{}
		// Source order, so the errors come in a fixed order. A columns
		// entry that is not in the source fails later in table().
		for _, info := range infos {
			low := strings.ToLower(info.Name)
			if !exprCols[low] {
				continue
			}
			var text string
			for k, c := range t.Columns {
				if strings.ToLower(k) == low {
					text, _ = c.Args["sql"].(string)
				}
			}
			refs, err := b.checkExpression(ctx, conn, text, cols, exprCols)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				errs = append(errs, fmt.Errorf("column %s: expression %w", info.Name, err))
				continue
			}
			checked[low] = exprColumn{sql: text, refs: refs}
		}
		if len(errs) > 0 {
			return fmt.Errorf("table %s: %w", t.Name, errors.Join(errs...))
		}
		b.exprs[strings.ToLower(t.Name)] = checked
	}
	return nil
}

// checkExpression parses text with DuckDB's own parser and walks the tree
// (plan 1.7). cols are the columns it may read by lower-case name,
// exprCols the expression columns. It returns the source names of the
// columns it reads. The error never holds DuckDB text.
func (b *builder) checkExpression(ctx context.Context, conn *sql.Conn, text string, cols map[string]string,
	exprCols map[string]bool) ([]string, error) {
	// The same text goes into the INSERT; a comment or a line break could
	// change the meaning of that statement.
	if strings.Contains(text, "--") || strings.Contains(text, "/*") || strings.ContainsAny(text, "\r\n;\x00") {
		return nil, errors.New("holds a comment, a line break or ;")
	}
	var out string
	err := conn.QueryRowContext(ctx, "SELECT CAST(json_serialize_sql(CAST(? AS VARCHAR)) AS VARCHAR)", "SELECT ("+text+")").Scan(&out)
	var tree map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(out), &tree)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		b.log.Debug("synthetic build: expression parse failed", "name", b.name, "error", err)
		return nil, errors.New("does not parse")
	}
	if tree["error"] != false {
		b.log.Debug("synthetic build: expression parse failed", "name", b.name, "error", tree["error_message"])
		return nil, errors.New("does not parse")
	}
	item, ok := selectItem(tree)
	if !ok {
		return nil, errors.New("is not a single expression")
	}
	w := exprWalk{cols: cols, exprCols: exprCols, refs: map[string]bool{}, funcs: map[string]bool{}}
	if err := w.walk(item); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(w.funcs))
	for n := range w.funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// Every row of the name must be a scalar function that gives the
		// same result for the same input. An operator only has to exist.
		var rows, bad int64
		err := conn.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE function_type IS DISTINCT FROM 'scalar'
  OR stability IS DISTINCT FROM 'CONSISTENT') FROM duckdb_functions() WHERE lower(function_name) = lower(?)`, n).Scan(&rows, &bad)
		if err != nil {
			return nil, b.duckErr("check an expression", err)
		}
		if rows == 0 || (w.funcs[n] && bad > 0) {
			return nil, fmt.Errorf("calls function %s, which is not allowed", n)
		}
	}
	refs := make([]string, 0, len(w.refs))
	for r := range w.refs {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs, nil
}

// selectItem returns the one select item of a parsed SELECT (...) with no
// FROM, WHERE, GROUP BY, HAVING, QUALIFY, modifiers, CTEs or parameters.
func selectItem(tree map[string]any) (map[string]any, bool) {
	stmts, _ := tree["statements"].([]any)
	if len(stmts) != 1 {
		return nil, false
	}
	st, _ := stmts[0].(map[string]any)
	if params, _ := st["named_param_map"].([]any); len(params) > 0 {
		return nil, false
	}
	n, _ := st["node"].(map[string]any)
	if n["type"] != "SELECT_NODE" {
		return nil, false
	}
	if from, _ := n["from_table"].(map[string]any); from["type"] != "EMPTY" {
		return nil, false
	}
	for _, k := range []string{"where_clause", "having", "qualify", "sample"} {
		if n[k] != nil {
			return nil, false
		}
	}
	for _, k := range []string{"group_expressions", "group_sets", "modifiers"} {
		if l, _ := n[k].([]any); len(l) > 0 {
			return nil, false
		}
	}
	if cte, _ := n["cte_map"].(map[string]any); cte != nil {
		if l, _ := cte["map"].([]any); len(l) > 0 {
			return nil, false
		}
	}
	list, _ := n["select_list"].([]any)
	if len(list) != 1 {
		return nil, false
	}
	item, ok := list[0].(map[string]any)
	return item, ok
}

// exprWalk checks every node of an expression tree.
type exprWalk struct {
	cols     map[string]string
	exprCols map[string]bool
	// refs are the source names read; funcs the lower-case function
	// names called, true when the name needs the scalar check.
	refs  map[string]bool
	funcs map[string]bool
}

// walk visits maps in sorted key order, so the first error is stable.
func (w *exprWalk) walk(v any) error {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if err := w.walk(e); err != nil {
				return err
			}
		}
	case map[string]any:
		if class, ok := x["class"].(string); ok {
			if err := w.node(class, x); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := w.walk(x[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *exprWalk) node(class string, x map[string]any) error {
	switch class {
	case "COLUMN_REF":
		parts, _ := x["column_names"].([]any)
		names := make([]string, len(parts))
		for i, p := range parts {
			names[i], _ = p.(string)
		}
		name := strings.Join(names, ".")
		low := strings.ToLower(name)
		if w.exprCols[low] {
			return fmt.Errorf("names column %s, which is an expression column", name)
		}
		src, ok := w.cols[low]
		if len(names) != 1 || !ok {
			return fmt.Errorf("names column %s, which is not a column of the table", name)
		}
		w.refs[src] = true
	case "FUNCTION":
		name, _ := x["function_name"].(string)
		low := strings.ToLower(name)
		if x["schema"] != "" || x["catalog"] != "" {
			return fmt.Errorf("calls function %s with a schema, which is not allowed", name)
		}
		orderBys, _ := x["order_bys"].(map[string]any)
		orders, _ := orderBys["orders"].([]any)
		if x["distinct"] != false || x["filter"] != nil || len(orders) > 0 {
			return fmt.Errorf("calls function %s with DISTINCT, FILTER or ORDER BY, which is not allowed", name)
		}
		op := x["is_operator"] == true
		if !op && (!exprFunctions[low] || clockFunctions[low]) {
			return fmt.Errorf("calls function %s, which is not allowed", name)
		}
		w.funcs[low] = w.funcs[low] || !op
	case "CAST":
		// A named type is looked up in a catalog, the source's included.
		if hasNamedType(x["cast_type"]) {
			return errors.New("casts to a named type, which is not allowed")
		}
	default:
		if exprClasses[class] {
			return nil
		}
		if t, ok := exprClassText[class]; ok {
			return fmt.Errorf("uses %s, which is not allowed", t)
		}
		return fmt.Errorf("uses an expression of class %s, which is not allowed", class)
	}
	return nil
}

// hasNamedType reports whether a parsed type holds a type DuckDB looks up
// by name.
func hasNamedType(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if hasNamedType(e) {
				return true
			}
		}
	case map[string]any:
		if id, _ := x["id"].(string); id == "UNBOUND" || id == "USER" {
			return true
		}
		for _, e := range x {
			if hasNamedType(e) {
				return true
			}
		}
	}
	return false
}

// exprValue is the snapshot value of expression column i: NULL where the
// staged marker is NULL, else the expression cast to the snapshot type.
func exprValue(i int, c *column) string {
	return "CASE WHEN r." + exprMarkerName(i) + " IS NULL THEN NULL ELSE CAST((" + c.expr.sql + ") AS " + c.snapType + ") END"
}

// snapshotSelect returns the SELECT that turns the staging table into the
// snapshot rows. Without expression columns it casts the staged columns.
func snapshotSelect(cols []*column) string {
	plain := make([]string, len(cols))
	outer := make([]string, len(cols))
	hasExpr := false
	for i, c := range cols {
		if c.expr != nil {
			hasExpr = true
			outer[i] = exprValue(i, c)
			continue
		}
		plain[i] = castExpr(stageName(i), c.snapType)
		outer[i] = "r." + parser.QuoteIdent(c.info.Name)
	}
	if !hasExpr {
		return "SELECT " + strings.Join(plain, ", ") + " FROM " + stageTable
	}
	return "SELECT " + strings.Join(outer, ", ") + " FROM (" + innerSelect(cols) + ") AS r"
}

// innerSelect casts the staged columns to the snapshot types under their
// own names, so an expression reads the snapshot values. A marker is not
// under its column name, so a reference to an expression column cannot
// bind.
func innerSelect(cols []*column) string {
	items := make([]string, len(cols))
	for i, c := range cols {
		if c.expr != nil {
			items[i] = stageName(i) + " AS " + exprMarkerName(i)
		} else {
			items[i] = castExpr(stageName(i), c.snapType) + " AS " + parser.QuoteIdent(c.info.Name)
		}
	}
	return "SELECT " + strings.Join(items, ", ") + " FROM " + stageTable
}

// hashExpressions adds the expression values of the new snapshot table
// to the content hash, as DuckDB text in insertion order.
func (b *builder) hashExpressions(ctx context.Context, conn *sql.Conn, target string, cols []*column) error {
	var sel []string
	for _, c := range cols {
		if c.expr != nil {
			sel = append(sel, "CAST("+parser.QuoteIdent(c.info.Name)+" AS VARCHAR)")
		}
	}
	if len(sel) == 0 {
		return nil
	}
	rows, err := conn.QueryContext(ctx, "SELECT "+strings.Join(sel, ", ")+" FROM "+target+" ORDER BY rowid")
	if err != nil {
		return b.duckErr("read the expression columns", err)
	}
	defer func() { _ = rows.Close() }()
	vals := make([]sql.NullString, len(sel))
	ptrs := make([]any, len(sel))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return b.duckErr("read the expression columns", err)
		}
		for _, v := range vals {
			var x any
			if v.Valid {
				x = v.String
			}
			if err := hashValue(b.hash, x); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return b.duckErr("read the expression columns", err)
	}
	return nil
}
