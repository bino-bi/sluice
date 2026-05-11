// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"slices"
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// extractShape builds the parser.QueryShape for raw. Only the top-level
// Stmt is inspected; values inside nested subqueries are ignored on
// purpose — QueryRejectPolicy rules key off the outer query.
func extractShape(raw *pg.RawStmt, tables []parser.TableRef, catalogs []string) parser.QueryShape {
	shape := parser.QueryShape{Catalogs: catalogs}
	if raw == nil || raw.Stmt == nil {
		return shape
	}
	switch v := raw.Stmt.Node.(type) {
	case *pg.Node_SelectStmt:
		if v.SelectStmt != nil {
			fillSelectShape(v.SelectStmt, tables, &shape)
		}
	case *pg.Node_ExplainStmt:
		if v.ExplainStmt != nil && v.ExplainStmt.Query != nil {
			if inner, ok := v.ExplainStmt.Query.Node.(*pg.Node_SelectStmt); ok && inner.SelectStmt != nil {
				fillSelectShape(inner.SelectStmt, tables, &shape)
			}
		}
	}
	return shape
}

// fillSelectShape populates shape from a top-level SelectStmt. For set
// operations the left arm is used as the representative (the rewriter
// inspects each arm separately when it needs to).
func fillSelectShape(s *pg.SelectStmt, tables []parser.TableRef, shape *parser.QueryShape) {
	if s.Larg != nil {
		// UNION / EXCEPT / INTERSECT — use the left arm for flags.
		shape.HasUnion = true
		fillSelectShape(s.Larg, tables, shape)
		return
	}

	shape.IsAggregate = len(s.GroupClause) > 0 || s.HavingClause != nil || hasAggFunc(s.TargetList)
	shape.HasCTE = s.WithClause != nil && len(s.WithClause.Ctes) > 0
	shape.HasOrderBy = len(s.SortClause) > 0
	shape.HasWhere = s.WhereClause != nil
	shape.GroupByColumns = columnRefNames(s.GroupClause)
	shape.WhereColumns = whereColumnNames(s.WhereClause)
	shape.Joins = countJoins(s.FromClause)
	shape.HasSelectStar = hasSelectStar(s.TargetList)

	if s.LimitCount != nil {
		shape.HasLimit = true
		shape.LimitValue = integerLiteral(s.LimitCount)
	}

	if s.WithClause != nil {
		shape.IsRecursiveCTE = s.WithClause.Recursive
	}

	_ = tables // Reserved for future cross-catalog detection.
}

// hasSelectStar reports whether any target-list entry is A_Star or a
// ColumnRef whose last field is A_Star.
func hasSelectStar(targets []*pg.Node) bool {
	for _, t := range targets {
		res, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || res.ResTarget == nil {
			continue
		}
		val := res.ResTarget.Val
		if val == nil {
			continue
		}
		switch v := val.Node.(type) {
		case *pg.Node_AStar:
			return true
		case *pg.Node_ColumnRef:
			if v.ColumnRef == nil {
				continue
			}
			fields := v.ColumnRef.Fields
			if len(fields) == 0 {
				continue
			}
			if _, ok := fields[len(fields)-1].Node.(*pg.Node_AStar); ok {
				return true
			}
		}
	}
	return false
}

// hasAggFunc reports whether any expression in targets uses a FuncCall
// with AggFilter or AggStar set (best-effort detection).
func hasAggFunc(targets []*pg.Node) bool {
	for _, t := range targets {
		res, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || res.ResTarget == nil {
			continue
		}
		if containsAgg(res.ResTarget.Val) {
			return true
		}
	}
	return false
}

// containsAgg recurses into an expression looking for aggregate-like
// FuncCall nodes. This is heuristic: COUNT(*), SUM(x), AVG(x) all match.
func containsAgg(n *pg.Node) bool {
	if n == nil {
		return false
	}
	switch v := n.Node.(type) {
	case *pg.Node_FuncCall:
		if v.FuncCall == nil {
			return false
		}
		if v.FuncCall.AggStar || v.FuncCall.AggDistinct {
			return true
		}
		if name := funcCallName(v.FuncCall); isAggregateFunc(name) {
			return true
		}
	case *pg.Node_AExpr:
		if v.AExpr != nil {
			return containsAgg(v.AExpr.Lexpr) || containsAgg(v.AExpr.Rexpr)
		}
	case *pg.Node_BoolExpr:
		if v.BoolExpr != nil {
			if slices.ContainsFunc(v.BoolExpr.Args, containsAgg) {
				return true
			}
		}
	}
	return false
}

// funcCallName returns the lowercased last-segment name of a FuncCall's
// Funcname list.
func funcCallName(fc *pg.FuncCall) string {
	if fc == nil || len(fc.Funcname) == 0 {
		return ""
	}
	last := fc.Funcname[len(fc.Funcname)-1]
	if last == nil {
		return ""
	}
	if s, ok := last.Node.(*pg.Node_String_); ok && s.String_ != nil {
		return s.String_.Sval
	}
	return ""
}

// isAggregateFunc reports whether name is a well-known aggregate. DuckDB
// supports additional aggregates (list, arg_max, …); v1 extends this set.
func isAggregateFunc(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max",
		"bit_and", "bit_or", "bit_xor",
		"string_agg", "array_agg", "json_agg", "jsonb_agg":
		return true
	}
	return false
}

// columnRefNames lifts ColumnRef names from a node list. Returns nil if
// no names were found.
func columnRefNames(ns []*pg.Node) []string {
	var out []string
	for _, n := range ns {
		if name := columnRefName(n); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// columnRefName extracts a dotted column name from a ColumnRef node.
// Non-ColumnRef inputs return "".
func columnRefName(n *pg.Node) string {
	if n == nil {
		return ""
	}
	cr, ok := n.Node.(*pg.Node_ColumnRef)
	if !ok || cr.ColumnRef == nil {
		return ""
	}
	parts := make([]string, 0, len(cr.ColumnRef.Fields))
	for _, f := range cr.ColumnRef.Fields {
		if f == nil {
			continue
		}
		if s, ok := f.Node.(*pg.Node_String_); ok && s.String_ != nil {
			parts = append(parts, s.String_.Sval)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return joinDotted(parts)
}

// joinDotted joins ident parts with a dot. Kept local to avoid pulling in
// strings for a one-off.
func joinDotted(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	n := len(parts) - 1
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '.')
		}
		out = append(out, p...)
	}
	return string(out)
}

// whereColumnNames walks the top-level WHERE tree and returns every
// column reference it finds. Best-effort — QueryRejectPolicy uses this
// for simple "no-where-clause" / "where-on-pii" checks.
func whereColumnNames(where *pg.Node) []string {
	if where == nil {
		return nil
	}
	var out []string
	var walk func(n *pg.Node)
	walk = func(n *pg.Node) {
		if n == nil {
			return
		}
		switch v := n.Node.(type) {
		case *pg.Node_ColumnRef:
			if name := columnRefName(n); name != "" {
				out = append(out, name)
			}
			_ = v
		case *pg.Node_AExpr:
			if v.AExpr != nil {
				walk(v.AExpr.Lexpr)
				walk(v.AExpr.Rexpr)
			}
		case *pg.Node_BoolExpr:
			if v.BoolExpr != nil {
				for _, a := range v.BoolExpr.Args {
					walk(a)
				}
			}
		case *pg.Node_NullTest:
			if v.NullTest != nil {
				walk(v.NullTest.Arg)
			}
		}
	}
	walk(where)
	return out
}

// countJoins counts join operators in a FROM clause list.
func countJoins(from []*pg.Node) int {
	n := 0
	var walk func(*pg.Node)
	walk = func(node *pg.Node) {
		if node == nil {
			return
		}
		if je, ok := node.Node.(*pg.Node_JoinExpr); ok && je.JoinExpr != nil {
			n++
			walk(je.JoinExpr.Larg)
			walk(je.JoinExpr.Rarg)
		}
	}
	for _, f := range from {
		walk(f)
	}
	return n
}

// integerLiteral extracts an int64 from an A_Const-wrapping Integer node,
// returning 0 when the value is not a plain integer literal.
func integerLiteral(n *pg.Node) int64 {
	if n == nil {
		return 0
	}
	c, ok := n.Node.(*pg.Node_AConst)
	if !ok || c.AConst == nil {
		return 0
	}
	switch v := c.AConst.Val.(type) {
	case *pg.A_Const_Ival:
		if v.Ival != nil {
			return int64(v.Ival.Ival)
		}
	case *pg.A_Const_Fval:
		if v.Fval != nil {
			if f, err := strconv.ParseFloat(v.Fval.Fval, 64); err == nil {
				return int64(f)
			}
		}
	}
	return 0
}
