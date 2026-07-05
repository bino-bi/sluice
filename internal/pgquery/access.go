// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"strconv"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// collectAccess deep-walks the whole statement collecting every referenced
// column and every (column, op, literal) comparison — through subqueries,
// CTEs, and set-operation arms. Unlike whereColumnNames (top-level only,
// used by QueryRejectPolicy), this feeds ApprovalPolicy triggers, which
// must be fail-closed: a column hidden in a subquery still counts.
func collectAccess(raw *pg.RawStmt) ([]string, []parser.Comparison) {
	c := &accessCollector{seenCol: map[string]struct{}{}, seenCmp: map[string]struct{}{}}
	if raw != nil {
		c.walkNode(raw.Stmt)
	}
	return c.columns, c.comparisons
}

type accessCollector struct {
	columns     []string
	comparisons []parser.Comparison
	seenCol     map[string]struct{}
	seenCmp     map[string]struct{}
}

func (c *accessCollector) addColumn(name string) {
	if name == "" {
		return
	}
	if _, ok := c.seenCol[name]; ok {
		return
	}
	c.seenCol[name] = struct{}{}
	c.columns = append(c.columns, name)
}

func (c *accessCollector) addComparison(cmp parser.Comparison) {
	key := cmp.Column + "\x00" + cmp.Op + "\x00" + cmp.Value
	if _, ok := c.seenCmp[key]; ok {
		return
	}
	c.seenCmp[key] = struct{}{}
	c.comparisons = append(c.comparisons, cmp)
}

// walkNode recurses through statement and expression nodes, harvesting
// columns everywhere and comparisons from predicate positions.
func (c *accessCollector) walkNode(n *pg.Node) {
	if n == nil || n.Node == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt:
		c.walkSelect(v.SelectStmt)
	case *pg.Node_ExplainStmt:
		if v.ExplainStmt != nil {
			c.walkNode(v.ExplainStmt.Query)
		}
	case *pg.Node_ColumnRef:
		c.addColumn(columnRefName(n))
	case *pg.Node_AExpr:
		c.walkAExpr(v.AExpr)
	case *pg.Node_BoolExpr:
		if v.BoolExpr != nil {
			for _, a := range v.BoolExpr.Args {
				c.walkNode(a)
			}
		}
	case *pg.Node_NullTest:
		if v.NullTest != nil {
			c.walkNode(v.NullTest.Arg)
			if col := columnRefName(v.NullTest.Arg); col != "" {
				c.addComparison(parser.Comparison{Column: col, Op: "isnull"})
			}
		}
	case *pg.Node_FuncCall:
		if v.FuncCall != nil {
			for _, a := range v.FuncCall.Args {
				c.walkNode(a)
			}
		}
	case *pg.Node_TypeCast:
		if v.TypeCast != nil {
			c.walkNode(v.TypeCast.Arg)
		}
	case *pg.Node_CaseExpr:
		if v.CaseExpr != nil {
			c.walkNode(v.CaseExpr.Arg)
			for _, w := range v.CaseExpr.Args {
				c.walkNode(w)
			}
			c.walkNode(v.CaseExpr.Defresult)
		}
	case *pg.Node_CaseWhen:
		if v.CaseWhen != nil {
			c.walkNode(v.CaseWhen.Expr)
			c.walkNode(v.CaseWhen.Result)
		}
	case *pg.Node_SubLink:
		if v.SubLink != nil {
			c.walkNode(v.SubLink.Testexpr)
			c.walkNode(v.SubLink.Subselect)
		}
	case *pg.Node_List:
		if v.List != nil {
			for _, item := range v.List.Items {
				c.walkNode(item)
			}
		}
	case *pg.Node_CoalesceExpr:
		if v.CoalesceExpr != nil {
			for _, a := range v.CoalesceExpr.Args {
				c.walkNode(a)
			}
		}
	}
}

func (c *accessCollector) walkSelect(s *pg.SelectStmt) {
	if s == nil {
		return
	}
	// Set operations: walk both arms.
	if s.Larg != nil || s.Rarg != nil {
		c.walkSelect(s.Larg)
		c.walkSelect(s.Rarg)
		return
	}
	if s.WithClause != nil {
		for _, cte := range s.WithClause.Ctes {
			if cc, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && cc.CommonTableExpr != nil {
				c.walkNode(cc.CommonTableExpr.Ctequery)
			}
		}
	}
	for _, t := range s.TargetList {
		if rt, ok := t.Node.(*pg.Node_ResTarget); ok && rt.ResTarget != nil {
			c.walkNode(rt.ResTarget.Val)
		}
	}
	for _, f := range s.FromClause {
		c.walkFrom(f)
	}
	c.walkNode(s.WhereClause)
	c.walkNode(s.HavingClause)
	for _, g := range s.GroupClause {
		c.addColumn(columnRefName(g))
		c.walkNode(g)
	}
	for _, o := range s.SortClause {
		if sb, ok := o.Node.(*pg.Node_SortBy); ok && sb.SortBy != nil {
			c.walkNode(sb.SortBy.Node)
		}
	}
}

func (c *accessCollector) walkFrom(n *pg.Node) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_JoinExpr:
		if v.JoinExpr != nil {
			c.walkFrom(v.JoinExpr.Larg)
			c.walkFrom(v.JoinExpr.Rarg)
			c.walkNode(v.JoinExpr.Quals)
		}
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil {
			c.walkNode(v.RangeSubselect.Subquery)
		}
	}
}

// walkAExpr harvests columns from both sides and, when it is a
// column-vs-literal comparison, records a normalised Comparison.
func (c *accessCollector) walkAExpr(e *pg.A_Expr) {
	if e == nil {
		return
	}
	c.walkNode(e.Lexpr)
	c.walkNode(e.Rexpr)

	op := aexprOp(e)
	if op == "" {
		return
	}
	// IN list: one comparison per literal.
	if e.Kind == pg.A_Expr_Kind_AEXPR_IN {
		col := columnRefName(e.Lexpr)
		if col == "" {
			return
		}
		for _, lit := range listLiterals(e.Rexpr) {
			c.addComparison(parser.Comparison{Column: col, Op: "in", Value: lit})
		}
		return
	}

	lcol := columnRefName(e.Lexpr)
	rcol := columnRefName(e.Rexpr)
	switch {
	case lcol != "" && rcol == "":
		if lit, ok := literalString(e.Rexpr); ok {
			c.addComparison(parser.Comparison{Column: lcol, Op: op, Value: lit})
		}
	case rcol != "" && lcol == "":
		if lit, ok := literalString(e.Lexpr); ok {
			c.addComparison(parser.Comparison{Column: rcol, Op: flipComparisonOp(op), Value: lit})
		}
	}
}

// aexprOp returns the normalised operator string for an A_Expr, or "" when
// it is not a scalar comparison we model.
func aexprOp(e *pg.A_Expr) string {
	switch e.Kind {
	case pg.A_Expr_Kind_AEXPR_OP:
		return normalizeOp(aexprName(e))
	case pg.A_Expr_Kind_AEXPR_IN:
		return "in"
	case pg.A_Expr_Kind_AEXPR_LIKE:
		return "like"
	case pg.A_Expr_Kind_AEXPR_ILIKE:
		return "ilike"
	default:
		return ""
	}
}

func aexprName(e *pg.A_Expr) string {
	if len(e.Name) == 0 {
		return ""
	}
	if s, ok := e.Name[len(e.Name)-1].Node.(*pg.Node_String_); ok && s.String_ != nil {
		return s.String_.Sval
	}
	return ""
}

func normalizeOp(sym string) string {
	switch sym {
	case "=", "!=", "<", "<=", ">", ">=":
		return sym
	case "<>":
		return "!="
	case "~~":
		return "like"
	case "~~*":
		return "ilike"
	default:
		return ""
	}
}

func flipComparisonOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	default:
		return op
	}
}

// literalString renders an A_Const literal to its string form. Returns
// (_, false) when n is not a literal.
func literalString(n *pg.Node) (string, bool) {
	if n == nil {
		return "", false
	}
	// Unwrap a TypeCast around a literal (e.g. '2024-01-01'::date).
	if tc, ok := n.Node.(*pg.Node_TypeCast); ok && tc.TypeCast != nil {
		return literalString(tc.TypeCast.Arg)
	}
	c, ok := n.Node.(*pg.Node_AConst)
	if !ok || c.AConst == nil {
		return "", false
	}
	if c.AConst.Isnull {
		return "", false
	}
	switch v := c.AConst.Val.(type) {
	case *pg.A_Const_Sval:
		if v.Sval != nil {
			return v.Sval.Sval, true
		}
	case *pg.A_Const_Ival:
		if v.Ival != nil {
			return strconv.FormatInt(int64(v.Ival.Ival), 10), true
		}
	case *pg.A_Const_Fval:
		if v.Fval != nil {
			return v.Fval.Fval, true
		}
	case *pg.A_Const_Boolval:
		if v.Boolval != nil {
			return strconv.FormatBool(v.Boolval.Boolval), true
		}
	}
	return "", false
}

// listLiterals renders the literal elements of an IN-list node.
func listLiterals(n *pg.Node) []string {
	if n == nil {
		return nil
	}
	lst, ok := n.Node.(*pg.Node_List)
	if !ok || lst.List == nil {
		return nil
	}
	var out []string
	for _, item := range lst.List.Items {
		if lit, ok := literalString(item); ok {
			out = append(out, lit)
		}
	}
	return out
}
