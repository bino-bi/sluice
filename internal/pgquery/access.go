// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"strconv"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// collectAccess walks every node of the statement for the ApprovalPolicy
// triggers, which must fail closed. Unlike whereColumnNames (used by
// QueryRejectPolicy), no expression form is skipped: a column in a
// subquery, a CTE, a set-operation arm or any function counts.
func collectAccess(raw *pg.RawStmt, shape *parser.QueryShape) {
	if raw == nil || raw.Stmt == nil {
		return
	}
	c := &accessCollector{
		seenCol:    map[string]struct{}{},
		seenUnread: map[string]struct{}{},
		seenCmp:    map[string]struct{}{},
		read:       map[*pg.ColumnRef]struct{}{},
		fromNames:  map[string]struct{}{},
	}
	c.walk(raw.Stmt.ProtoReflect(), false, false)
	// A column reference named like a FROM item is the whole row of that
	// item: SELECT p FROM people p.
	for _, col := range c.columns {
		if _, ok := c.fromNames[col[strings.LastIndexByte(col, '.')+1:]]; ok {
			c.all = true
		}
	}
	shape.AccessedColumns = c.columns
	shape.Comparisons = c.comparisons
	shape.UnreadPredicateColumns = c.unread
	shape.AccessesAllColumns = c.all
}

type accessCollector struct {
	columns     []string
	unread      []string
	comparisons []parser.Comparison
	seenCol     map[string]struct{}
	seenUnread  map[string]struct{}
	seenCmp     map[string]struct{}
	// read holds the column side of each recorded comparison.
	read      map[*pg.ColumnRef]struct{}
	fromNames map[string]struct{}
	all       bool
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

func (c *accessCollector) addUnread(name string) {
	if name == "" {
		return
	}
	if _, ok := c.seenUnread[name]; ok {
		return
	}
	c.seenUnread[name] = struct{}{}
	c.unread = append(c.unread, name)
}

func (c *accessCollector) addComparison(cmp parser.Comparison) {
	key := cmp.Column + "\x00" + cmp.Op + "\x00" + cmp.Value
	if _, ok := c.seenCmp[key]; ok {
		return
	}
	c.seenCmp[key] = struct{}{}
	c.comparisons = append(c.comparisons, cmp)
}

// walk visits m and every message below it. cond is set below a condition:
// a column there decides which rows or values come back. plain is set while
// the path from WHERE, HAVING, ON or FILTER holds only AND and OR. Only a
// comparison in a plain position is read as (column, op, value); a negation
// or a CASE around it would change what it means. A column in a condition
// that no such comparison reads is reported as unread.
func (c *accessCollector) walk(m protoreflect.Message, cond, plain bool) {
	switch v := m.Interface().(type) {
	case *pg.A_Star:
		c.all = true
	case *pg.ColumnRef:
		name := columnRefNameOf(v)
		c.addColumn(name)
		if _, ok := c.read[v]; cond && !ok {
			c.addUnread(name)
		}
	case *pg.A_Expr:
		c.readComparison(v, plain)
	case *pg.NullTest:
		if col := columnRefName(v.Arg); col != "" {
			c.addComparison(parser.Comparison{Column: col, Op: "isnull"})
			if plain {
				c.read[v.Arg.GetColumnRef()] = struct{}{}
			}
		}
	case *pg.RangeVar:
		name := v.Relname
		if v.Alias != nil {
			name = v.Alias.Aliasname
			// p(a, b) renames the columns: a reads the first one.
			if len(v.Alias.Colnames) > 0 {
				c.all = true
			}
		}
		c.fromNames[name] = struct{}{}
	case *pg.JoinExpr:
		// NATURAL compares every common column without naming one.
		if v.IsNatural {
			c.all = true
		}
		for _, u := range v.UsingClause {
			if str := u.GetString_(); str != nil {
				c.addColumn(str.Sval)
				c.addUnread(str.Sval)
			}
		}
	}
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		childCond, childPlain := childContext(m.Interface(), fd.Name(), cond, plain)
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				c.walk(list.Get(i).Message(), childCond, childPlain)
			}
			return true
		}
		c.walk(val.Message(), childCond, childPlain)
		return true
	})
}

// childContext returns the cond and plain flags for the field of parent.
func childContext(parent protoreflect.ProtoMessage, field protoreflect.Name, cond, plain bool) (bool, bool) {
	switch p := parent.(type) {
	case *pg.Node:
		return cond, plain
	case *pg.SelectStmt:
		if field == "where_clause" || field == "having_clause" {
			return true, true
		}
	case *pg.JoinExpr:
		if field == "quals" {
			return true, true
		}
	case *pg.FuncCall:
		if field == "agg_filter" {
			return true, true
		}
	case *pg.BoolExpr:
		return true, plain && p.Boolop != pg.BoolExprType_NOT_EXPR
	case *pg.A_Expr:
		// Arithmetic and || build a value. Every other operator compares.
		return cond || !isValueOperator(p), false
	case *pg.NullTest, *pg.BooleanTest:
		return true, false
	case *pg.CaseWhen:
		return cond || field == "expr", false
	case *pg.CaseExpr:
		return cond || field == "arg", false
	case *pg.SubLink:
		return cond || field == "testexpr", false
	}
	return cond, false
}

func isValueOperator(e *pg.A_Expr) bool {
	if e.Kind != pg.A_Expr_Kind_AEXPR_OP {
		return false
	}
	switch aexprName(e) {
	case "+", "-", "*", "/", "%", "^", "||":
		return true
	}
	return false
}

// readComparison records a column-vs-literal comparison. In a plain
// position it also marks the column as read.
func (c *accessCollector) readComparison(e *pg.A_Expr, plain bool) {
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
		lits, allLiterals := listLiterals(e.Rexpr)
		for _, lit := range lits {
			c.addComparison(parser.Comparison{Column: col, Op: "in", Value: lit})
		}
		if plain && allLiterals {
			c.read[e.Lexpr.GetColumnRef()] = struct{}{}
		}
		return
	}

	lcol := columnRefName(e.Lexpr)
	rcol := columnRefName(e.Rexpr)
	switch {
	case lcol != "" && rcol == "":
		if lit, ok := literalString(e.Rexpr); ok {
			c.addComparison(parser.Comparison{Column: lcol, Op: op, Value: lit})
			if plain {
				c.read[e.Lexpr.GetColumnRef()] = struct{}{}
			}
		}
	case rcol != "" && lcol == "":
		if lit, ok := literalString(e.Lexpr); ok {
			c.addComparison(parser.Comparison{Column: rcol, Op: flipComparisonOp(op), Value: lit})
			if plain {
				c.read[e.Rexpr.GetColumnRef()] = struct{}{}
			}
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

// listLiterals renders the literal elements of an IN-list node, and
// reports whether every element is a literal.
func listLiterals(n *pg.Node) ([]string, bool) {
	if n == nil {
		return nil, false
	}
	lst, ok := n.Node.(*pg.Node_List)
	if !ok || lst.List == nil {
		return nil, false
	}
	var out []string
	for _, item := range lst.List.Items {
		if lit, ok := literalString(item); ok {
			out = append(out, lit)
		}
	}
	return out, len(out) == len(lst.List.Items)
}
