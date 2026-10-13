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
// triggers. Unlike whereColumnNames (used by QueryRejectPolicy), no
// expression form is skipped: a column in a subquery, a CTE, a
// set-operation arm or any function counts. Names are folded to lower
// case, since DuckDB resolves them without regard to case, also when
// quoted.
func collectAccess(raw *pg.RawStmt, shape *parser.QueryShape) {
	if raw == nil || raw.Stmt == nil {
		return
	}
	c := &accessCollector{
		seenCol:    map[string]struct{}{},
		seenUnread: map[string]struct{}{},
		seenCmp:    map[string]struct{}{},
		read:       map[*pg.ColumnRef]struct{}{},
		ctes:       map[string]struct{}{},
		exists:     map[*pg.SelectStmt]struct{}{},
		outputRefs: map[*pg.ColumnRef]struct{}{},
		rowNames:   map[string]struct{}{},
	}
	root := raw.Stmt.ProtoReflect()
	// The walk order is not the text order, so collect first what a
	// reference must be checked against.
	walkMessages(root, func(m protoreflect.Message) bool {
		switch v := m.Interface().(type) {
		case *pg.CommonTableExpr:
			c.ctes[strings.ToLower(v.Ctename)] = struct{}{}
		case *pg.SubLink:
			if sel := v.Subselect.GetSelectStmt(); sel != nil && v.SubLinkType == pg.SubLinkType_EXISTS_SUBLINK {
				c.exists[sel] = struct{}{}
			}
		}
		return true
	})
	c.walk(root, false, false, false)
	// A reference whose last part names a table, its alias or a join alias
	// can be that item as a row value: SELECT p FROM people p, p.*,
	// main.people. A qualifier does not rule it out, since a schema and a
	// FROM item can share a name.
	for _, col := range c.columns {
		if _, ok := c.rowNames[col[strings.LastIndexByte(col, '.')+1:]]; ok {
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
	// read holds the column side of each comparison in a plain position.
	read map[*pg.ColumnRef]struct{}
	// ctes holds the CTE names, exists the subqueries of EXISTS.
	ctes   map[string]struct{}
	exists map[*pg.SelectStmt]struct{}
	// outputRefs holds the ORDER BY items that name an output column of
	// their own SELECT. They read no column of a table.
	outputRefs map[*pg.ColumnRef]struct{}
	// rowNames holds the names that give the columns of a table when used
	// as a value: a table, its alias, a join alias.
	rowNames map[string]struct{}
	all      bool
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

// walk visits m and every message below it.
//
// star is set while a star in the current SELECT reads the columns of a
// table: the SELECT has a table in FROM, or no FROM at all. A star over a
// subquery or a CTE reads only what that names.
//
// cond is set below a condition: a column there decides which rows or
// values come back. plain is set while the path from WHERE, HAVING, ON or
// FILTER holds only AND and OR. Only a comparison in a plain position is
// read as (column, op, value); a negation or a CASE around it would change
// what it means. A column in a condition that no such comparison reads is
// reported as unread.
func (c *accessCollector) walk(m protoreflect.Message, star, cond, plain bool) {
	switch v := m.Interface().(type) {
	case *pg.SelectStmt:
		_, inExists := c.exists[v]
		star = !inExists && (len(v.FromClause) == 0 || c.fromHasTable(v.FromClause))
		c.findOutputRefs(v)
	case *pg.A_Star:
		if star {
			c.all = true
		}
	case *pg.ColumnRef:
		if _, ok := c.outputRefs[v]; ok {
			return
		}
		name := strings.ToLower(columnRefNameOf(v))
		c.addColumn(name)
		// q.* reads what q is: the name check after the walk decides. A
		// bare star has no name and goes on to the A_Star case.
		if n := len(v.Fields); name != "" && v.Fields[n-1].GetAStar() != nil {
			return
		}
		if _, ok := c.read[v]; cond && !ok {
			c.addUnread(name)
		}
	case *pg.A_Indirection:
		// (p).ssn names the field ssn.
		for _, ind := range v.Indirection {
			if str := ind.GetString_(); str != nil {
				name := strings.ToLower(str.Sval)
				c.addColumn(name)
				if cond {
					c.addUnread(name)
				}
			}
		}
	case *pg.A_Expr:
		// #3 is the third column of the FROM items. pg_query reads an
		// operator before it as one name: ||#3 is the operator "||#".
		if strings.Contains(aexprName(v), "#") {
			c.all = true
		}
		c.readComparison(v, plain)
	case *pg.NullTest:
		if col := columnRefName(v.Arg); col != "" {
			c.addComparison(parser.Comparison{Column: strings.ToLower(col), Op: "isnull"})
			if plain {
				c.read[v.Arg.GetColumnRef()] = struct{}{}
			}
		}
	case *pg.RangeVar:
		if c.isTable(v) {
			c.rowItem(v.Relname, v.Alias)
		}
	case *pg.JoinExpr:
		c.rowItem("", v.Alias)
		// NATURAL compares every common column without naming one.
		if v.IsNatural {
			c.all = true
		}
		for _, u := range v.UsingClause {
			if str := u.GetString_(); str != nil {
				name := strings.ToLower(str.Sval)
				c.addColumn(name)
				c.addUnread(name)
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
				c.walk(list.Get(i).Message(), star, childCond, childPlain)
			}
			return true
		}
		c.walk(val.Message(), star, childCond, childPlain)
		return true
	})
}

// isTable reports whether rv names a table. A name without schema that is
// a CTE name is a reference to that CTE.
func (c *accessCollector) isTable(rv *pg.RangeVar) bool {
	if rv.Schemaname != "" || rv.Catalogname != "" {
		return true
	}
	_, cte := c.ctes[strings.ToLower(rv.Relname)]
	return !cte
}

// fromHasTable reports whether a FROM clause reads a table directly or
// through joins.
func (c *accessCollector) fromHasTable(from []*pg.Node) bool {
	for _, n := range from {
		if rv := n.GetRangeVar(); rv != nil && c.isTable(rv) {
			return true
		}
		if j := n.GetJoinExpr(); j != nil && c.fromHasTable([]*pg.Node{j.Larg, j.Rarg}) {
			return true
		}
	}
	return false
}

// rowItem records the name of a table or a join: used as a value, the
// name gives its columns.
func (c *accessCollector) rowItem(name string, alias *pg.Alias) {
	if alias != nil {
		name = alias.Aliasname
		// p(a, b) renames the columns: a reads the first one.
		if len(alias.Colnames) > 0 {
			c.all = true
		}
	}
	if name != "" {
		c.rowNames[strings.ToLower(name)] = struct{}{}
	}
}

// findOutputRefs marks the ORDER BY items of sel that are the bare name of
// one of its output columns, as in count(*) AS orders ... ORDER BY orders.
// A set operation is left out: its ORDER BY belongs to no single SELECT.
func (c *accessCollector) findOutputRefs(sel *pg.SelectStmt) {
	if sel.Larg != nil || len(sel.SortClause) == 0 {
		return
	}
	outputs := map[string]struct{}{}
	for _, t := range sel.TargetList {
		if name := t.GetResTarget().GetName(); name != "" {
			outputs[strings.ToLower(name)] = struct{}{}
		}
	}
	for _, s := range sel.SortClause {
		cr := s.GetSortBy().GetNode().GetColumnRef()
		if cr == nil || len(cr.Fields) != 1 {
			continue
		}
		if _, ok := outputs[strings.ToLower(columnRefNameOf(cr))]; ok {
			c.outputRefs[cr] = struct{}{}
		}
	}
}

// childContext returns the cond and plain flags for the field of parent.
func childContext(parent protoreflect.ProtoMessage, field protoreflect.Name, cond, plain bool) (bool, bool) {
	// A clause starts plain unless a negation, a CASE or a comparison
	// around its subquery or aggregate could turn its meaning.
	clause := !cond || plain
	switch p := parent.(type) {
	case *pg.Node:
		return cond, plain
	case *pg.SelectStmt:
		if field == "where_clause" || field == "having_clause" {
			return true, clause
		}
	case *pg.JoinExpr:
		if field == "quals" {
			return true, clause
		}
	case *pg.FuncCall:
		if field == "agg_filter" {
			return true, clause
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
	case "+", "-", "*", "/", "//", "%", "^", "**", "||", "&", "|", "<<", ">>", "->", "->>":
		return true
	}
	return false
}

// readComparison records a column-vs-literal comparison. In a plain
// position it also marks the column as read, unless the literal has a
// cast: a cast can change the value that is compared.
func (c *accessCollector) readComparison(e *pg.A_Expr, plain bool) {
	op := aexprOp(e)
	if op == "" {
		return
	}
	// IN list: one comparison per literal.
	if e.Kind == pg.A_Expr_Kind_AEXPR_IN {
		col := strings.ToLower(columnRefName(e.Lexpr))
		if col == "" {
			return
		}
		lits, allPlain := listLiterals(e.Rexpr)
		for _, lit := range lits {
			c.addComparison(parser.Comparison{Column: col, Op: "in", Value: lit})
		}
		if plain && allPlain {
			c.read[e.Lexpr.GetColumnRef()] = struct{}{}
		}
		return
	}

	col, lit := e.Lexpr, e.Rexpr
	if columnRefName(col) == "" {
		col, lit = e.Rexpr, e.Lexpr
		op = flipComparisonOp(op)
	}
	name := strings.ToLower(columnRefName(col))
	value, ok := literalString(lit)
	if name == "" || !ok {
		return
	}
	c.addComparison(parser.Comparison{Column: name, Op: op, Value: value})
	if plain && lit.GetTypeCast() == nil {
		c.read[col.GetColumnRef()] = struct{}{}
	}
}

// aexprOp returns the normalised operator string for an A_Expr, or "" when
// it is not a scalar comparison we model. NOT IN, NOT LIKE and NOT ILIKE
// are not modelled.
func aexprOp(e *pg.A_Expr) string {
	name := aexprName(e)
	switch {
	case e.Kind == pg.A_Expr_Kind_AEXPR_OP:
		return normalizeOp(name)
	case e.Kind == pg.A_Expr_Kind_AEXPR_IN && name == "=":
		return "in"
	case e.Kind == pg.A_Expr_Kind_AEXPR_LIKE && name == "~~":
		return "like"
	case e.Kind == pg.A_Expr_Kind_AEXPR_ILIKE && name == "~~*":
		return "ilike"
	}
	return ""
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
// reports whether every element is a literal without a cast.
func listLiterals(n *pg.Node) ([]string, bool) {
	if n == nil {
		return nil, false
	}
	lst, ok := n.Node.(*pg.Node_List)
	if !ok || lst.List == nil {
		return nil, false
	}
	var out []string
	plain := true
	for _, item := range lst.List.Items {
		lit, ok := literalString(item)
		if ok {
			out = append(out, lit)
		}
		plain = plain && ok && item.GetTypeCast() == nil
	}
	return out, plain
}
