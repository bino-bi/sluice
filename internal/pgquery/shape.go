// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// extractShape builds the parser.QueryShape for raw. The values feed
// QueryRejectPolicy rules and conditions; fillSelectShape states what each
// one covers.
func extractShape(raw *pg.RawStmt, tables []parser.TableRef, catalogs []string) parser.QueryShape {
	shape := parser.QueryShape{Catalogs: catalogs}
	if raw == nil || raw.Stmt == nil {
		return shape
	}
	collectAccess(raw, &shape)
	shape.Functions, shape.UnsupportedFrom, shape.HasParams = collectFunctions(raw)
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

// fillSelectShape populates shape from the top-level SelectStmt:
//
//   - ORDER BY and LIMIT are the clauses of the whole statement, not those
//     of one SELECT of a set operation.
//   - HasWhere and IsAggregate hold only when every SELECT of a set
//     operation has them, since a rule uses them to require a WHERE or an
//     aggregate. HasSelectStar holds when any of them has a star. The
//     column lists cover all of them. Subqueries are not read.
//   - A WITH and a join count at any depth. A comma between FROM items is
//     a join.
func fillSelectShape(s *pg.SelectStmt, tables []parser.TableRef, shape *parser.QueryShape) {
	shape.HasUnion = s.Larg != nil
	shape.HasOrderBy = len(s.SortClause) > 0
	if s.LimitCount != nil {
		shape.HasLimit = true
		shape.LimitValue = integerLiteral(s.LimitCount)
	}

	shape.HasWhere, shape.IsAggregate = true, true
	for _, arm := range setOperationArms(s, nil) {
		shape.HasSelectStar = shape.HasSelectStar || hasSelectStar(arm.TargetList)
		shape.HasWhere = shape.HasWhere && arm.WhereClause != nil
		shape.IsAggregate = shape.IsAggregate &&
			(len(arm.GroupClause) > 0 || arm.HavingClause != nil || hasAggFunc(arm.TargetList))
		shape.WhereColumns = append(shape.WhereColumns, whereColumnNames(arm.WhereClause)...)
		shape.GroupByColumns = append(shape.GroupByColumns, columnRefNames(arm.GroupClause)...)
	}

	walkMessages(s.ProtoReflect(), func(m protoreflect.Message) bool {
		switch v := m.Interface().(type) {
		case *pg.WithClause:
			if len(v.Ctes) > 0 {
				shape.HasCTE = true
			}
			shape.IsRecursiveCTE = shape.IsRecursiveCTE || v.Recursive
		case *pg.JoinExpr:
			shape.Joins++
		case *pg.SelectStmt:
			if n := len(v.FromClause); n > 1 {
				shape.Joins += n - 1
			}
		}
		return true
	})

	_ = tables // Reserved for future cross-catalog detection.
}

// setOperationArms appends the SELECTs of a set operation to out, left to
// right. A plain SELECT is its own single arm.
func setOperationArms(s *pg.SelectStmt, out []*pg.SelectStmt) []*pg.SelectStmt {
	if s == nil {
		return out
	}
	if s.Larg == nil && s.Rarg == nil {
		return append(out, s)
	}
	return setOperationArms(s.Rarg, setOperationArms(s.Larg, out))
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

// hasAggFunc reports whether an expression in targets calls an aggregate,
// at any depth: in a function argument, a cast or CASE. A window call such
// as count(*) OVER () does not count, and neither does a subquery.
func hasAggFunc(targets []*pg.Node) bool {
	found := false
	for _, t := range targets {
		walkExpr(t, func(m protoreflect.Message) {
			if fc, ok := m.Interface().(*pg.FuncCall); ok && fc.Over == nil && isAggregateCall(fc) {
				found = true
			}
		})
	}
	return found
}

// isAggregateCall reports whether fc is an aggregate call: a DuckDB
// aggregate by name, or a call with *, DISTINCT, FILTER or WITHIN GROUP.
func isAggregateCall(fc *pg.FuncCall) bool {
	if fc.AggStar || fc.AggDistinct || fc.AggFilter != nil || fc.AggWithinGroup {
		return true
	}
	if n := len(fc.Funcname); n > 0 {
		return parser.IsAggregateFunction(fc.Funcname[n-1].GetString_().GetSval())
	}
	return false
}

// walkExpr visits n and every message below it. It does not enter the
// body of a subquery: that is another SELECT with its own shape.
func walkExpr(n *pg.Node, visit func(protoreflect.Message)) {
	if n != nil {
		walkExprMessage(n.ProtoReflect(), visit)
	}
}

func walkExprMessage(m protoreflect.Message, visit func(protoreflect.Message)) {
	visit(m)
	if sl, ok := m.Interface().(*pg.SubLink); ok {
		walkExpr(sl.Testexpr, visit)
		return
	}
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				walkExprMessage(list.Get(i).Message(), visit)
			}
			return true
		}
		walkExprMessage(val.Message(), visit)
		return true
	})
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
	return columnRefNameOf(cr.ColumnRef)
}

// columnRefNameOf joins the name parts of cr. A star is not a part: t.*
// gives "t".
func columnRefNameOf(cr *pg.ColumnRef) string {
	parts := make([]string, 0, len(cr.Fields))
	for _, f := range cr.Fields {
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

// whereColumnNames returns every column reference of the WHERE tree, at
// any depth, but not those in the body of a subquery. QueryRejectPolicy
// reads it as query.where_columns.
func whereColumnNames(where *pg.Node) []string {
	var out []string
	walkExpr(where, func(m protoreflect.Message) {
		if cr, ok := m.Interface().(*pg.ColumnRef); ok {
			if name := columnRefNameOf(cr); name != "" {
				out = append(out, name)
			}
		}
	})
	return out
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
