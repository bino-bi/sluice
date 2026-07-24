// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"fmt"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/policy"
)

// applyInjectFilter walks every SelectStmt's FROM tree and replaces each
// RangeVar whose table key has a compiled row filter with a
// RangeSubselect wrapping `SELECT * FROM <original> WHERE <pred>`. The
// original alias is preserved so outer column references keep resolving.
func (s *state) applyInjectFilter(raw *pg.ParseResult) error {
	if raw == nil || len(s.decision.RowFilters) == 0 {
		return nil
	}
	for _, stmt := range raw.Stmts {
		if stmt == nil {
			continue
		}
		if err := s.injectInNode(stmt.Stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) injectInNode(n *pg.Node) error {
	if n == nil {
		return nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt:
		if v.SelectStmt != nil {
			if err := s.injectInSelect(v.SelectStmt); err != nil {
				return err
			}
		}
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil && v.RangeSubselect.Subquery != nil {
			if err := s.injectInNode(v.RangeSubselect.Subquery); err != nil {
				return err
			}
		}
	case *pg.Node_ExplainStmt:
		// Policies match tables inside EXPLAIN bodies (the table walker
		// descends into them), so the rewrite must too.
		if v.ExplainStmt != nil {
			if err := s.injectInNode(v.ExplainStmt.Query); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *state) injectInSelect(sel *pg.SelectStmt) error {
	if sel == nil {
		return nil
	}
	if sel.WithClause != nil {
		for _, cte := range sel.WithClause.Ctes {
			if cte == nil {
				continue
			}
			if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
				if err := s.injectInNode(c.CommonTableExpr.Ctequery); err != nil {
					return err
				}
			}
		}
	}
	for i, from := range sel.FromClause {
		replaced, err := s.injectInFromItem(from)
		if err != nil {
			return err
		}
		sel.FromClause[i] = replaced
	}
	if sel.Larg != nil {
		if err := s.injectInSelect(sel.Larg); err != nil {
			return err
		}
	}
	if sel.Rarg != nil {
		if err := s.injectInSelect(sel.Rarg); err != nil {
			return err
		}
	}
	return nil
}

// injectInFromItem maybe-wraps a FROM entry. Returns the (possibly
// rewritten) node to put back into the parent slice.
func (s *state) injectInFromItem(n *pg.Node) (*pg.Node, error) {
	if n == nil {
		return n, nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		return s.maybeWrapRangeVar(v.RangeVar)
	case *pg.Node_JoinExpr:
		if v.JoinExpr == nil {
			return n, nil
		}
		larg, err := s.injectInFromItem(v.JoinExpr.Larg)
		if err != nil {
			return nil, err
		}
		rarg, err := s.injectInFromItem(v.JoinExpr.Rarg)
		if err != nil {
			return nil, err
		}
		v.JoinExpr.Larg = larg
		v.JoinExpr.Rarg = rarg
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil && v.RangeSubselect.Subquery != nil {
			if err := s.injectInNode(v.RangeSubselect.Subquery); err != nil {
				return nil, err
			}
		}
	}
	return n, nil
}

// maybeWrapRangeVar checks whether rv has an active row filter. If so,
// it returns a RangeSubselect wrapping `SELECT * FROM <rv> WHERE <pred>`
// with the original alias preserved. Otherwise the original RangeVar is
// returned verbatim.
func (s *state) maybeWrapRangeVar(rv *pg.RangeVar) (*pg.Node, error) {
	if rv == nil {
		return nil, nil
	}
	catalog := rv.Catalogname
	if catalog == "" {
		catalog = s.defaultCatalog
	}
	key := catalog + "." + rv.Schemaname + "." + rv.Relname
	filter, ok := s.decision.RowFilters[key]
	if !ok {
		return &pg.Node{Node: &pg.Node_RangeVar{RangeVar: rv}}, nil
	}

	// Preserve the existing alias; if none, synthesize one from the
	// relation name so outer references still resolve.
	alias := rv.Alias
	if alias == nil || alias.Aliasname == "" {
		alias = &pg.Alias{Aliasname: rv.Relname}
	}

	// Build: SELECT * FROM <original> WHERE <pred>
	inner := &pg.SelectStmt{
		TargetList: []*pg.Node{selectStarTarget()},
		FromClause: []*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: rv}}},
	}

	whereExpr, err := s.renderPredicate(filter.Predicate)
	if err != nil {
		return nil, fmt.Errorf("row filter %s: %w", key, err)
	}
	if whereExpr != nil {
		inner.WhereClause = whereExpr
	}

	// Strip the alias from the inner RangeVar so the outer RangeSubselect
	// owns it — avoids "alias specified more than once" at deparse time.
	if v, ok := inner.FromClause[0].Node.(*pg.Node_RangeVar); ok && v.RangeVar != nil {
		inner.FromClause[0] = &pg.Node{Node: &pg.Node_RangeVar{RangeVar: &pg.RangeVar{
			Catalogname: v.RangeVar.Catalogname,
			Schemaname:  v.RangeVar.Schemaname,
			Relname:     v.RangeVar.Relname,
			Inh:         v.RangeVar.Inh,
			Location:    v.RangeVar.Location,
		}}}
	}

	wrapped := &pg.RangeSubselect{
		Subquery: &pg.Node{Node: &pg.Node_SelectStmt{SelectStmt: inner}},
		Alias:    alias,
	}
	s.rewrites = append(s.rewrites, "row-filter-wrapped:"+key)
	return &pg.Node{Node: &pg.Node_RangeSubselect{RangeSubselect: wrapped}}, nil
}

// selectStarTarget builds a ResTarget whose Val is a lone `*`.
func selectStarTarget() *pg.Node {
	cr := &pg.ColumnRef{Fields: []*pg.Node{{Node: &pg.Node_AStar{AStar: &pg.A_Star{}}}}}
	rt := &pg.ResTarget{Val: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: cr}}}
	return &pg.Node{Node: &pg.Node_ResTarget{ResTarget: rt}}
}

// renderPredicate walks the compiled predicate tree and emits a WHERE
// expression AST. Templates are rendered against the current user and
// appended to s.params as positional `$N` placeholders.
func (s *state) renderPredicate(p *policy.CompiledPredicate) (*pg.Node, error) {
	if p == nil {
		return nil, nil
	}
	if len(p.All) > 0 {
		return s.renderBool(p.All, pg.BoolExprType_AND_EXPR)
	}
	if len(p.Any) > 0 {
		return s.renderBool(p.Any, pg.BoolExprType_OR_EXPR)
	}
	if p.Not != nil {
		inner, err := s.renderPredicate(p.Not)
		if err != nil {
			return nil, err
		}
		return &pg.Node{Node: &pg.Node_BoolExpr{BoolExpr: &pg.BoolExpr{
			Boolop: pg.BoolExprType_NOT_EXPR,
			Args:   []*pg.Node{inner},
		}}}, nil
	}
	return s.renderLeaf(p)
}

func (s *state) renderBool(children []*policy.CompiledPredicate, op pg.BoolExprType) (*pg.Node, error) {
	args := make([]*pg.Node, 0, len(children))
	for _, c := range children {
		n, err := s.renderPredicate(c)
		if err != nil {
			return nil, err
		}
		if n != nil {
			args = append(args, n)
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) == 1 {
		return args[0], nil
	}
	return &pg.Node{Node: &pg.Node_BoolExpr{BoolExpr: &pg.BoolExpr{Boolop: op, Args: args}}}, nil
}

// renderLeaf builds a Postgres AST fragment for one leaf predicate. All
// values are emitted as ParamRef placeholders; s.params retains the
// resolved Go values in positional order.
func (s *state) renderLeaf(p *policy.CompiledPredicate) (*pg.Node, error) {
	col := columnRefFromString(p.Column)
	switch string(p.Op) {
	case "IsNull", "IsNotNull":
		return s.renderNullCheck(col, string(p.Op)), nil
	}
	values, err := s.renderValues(p.Values)
	if err != nil {
		return nil, err
	}
	return s.renderBinary(col, string(p.Op), values)
}

func (s *state) renderNullCheck(col *pg.Node, op string) *pg.Node {
	// NULL checks deparse correctly as A_Expr with the op name ("IS" / "IS NOT").
	// pg_query_go models NULL tests as a BooleanTest or A_Expr; an A_Expr with
	// a NULL literal on the right side round-trips.
	name := "IS"
	if op == "IsNotNull" {
		name = "IS NOT"
	}
	nullConst := &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{Isnull: true}}}
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  pg.A_Expr_Kind_AEXPR_OP,
		Name:  []*pg.Node{pg.MakeStrNode(name)},
		Lexpr: col,
		Rexpr: nullConst,
	}}}
}

func (s *state) renderValues(vs []policy.ValueSource) ([]*pg.Node, error) {
	out := make([]*pg.Node, 0, len(vs))
	for _, v := range vs {
		if v.Template != nil {
			val, err := v.Template.Render(s.user, s.facts)
			if err != nil {
				return nil, err
			}
			s.params = append(s.params, val)
			out = append(out, paramRef(len(s.params)))
			continue
		}
		s.params = append(s.params, v.Literal)
		out = append(out, paramRef(len(s.params)))
	}
	return out, nil
}

func (s *state) renderBinary(col *pg.Node, op string, values []*pg.Node) (*pg.Node, error) {
	switch op {
	case "Equals":
		return opExpr(col, "=", values[0]), nil
	case "NotEquals":
		return opExpr(col, "<>", values[0]), nil
	case "GreaterThan":
		return opExpr(col, ">", values[0]), nil
	case "GreaterThanOrEqual":
		return opExpr(col, ">=", values[0]), nil
	case "LessThan":
		return opExpr(col, "<", values[0]), nil
	case "LessThanOrEqual":
		return opExpr(col, "<=", values[0]), nil
	case "Like":
		return opExpr(col, "~~", values[0]), nil
	case "NotLike":
		return opExpr(col, "!~~", values[0]), nil
	case "In":
		return inExpr(col, values, false), nil
	case "NotIn":
		return inExpr(col, values, true), nil
	case "Between":
		return betweenExpr(col, values[0], values[1], false), nil
	case "StartsWith":
		return funcExpr("starts_with", col, values[0]), nil
	case "EndsWith":
		return funcExpr("ends_with", col, values[0]), nil
	case "Contains":
		return funcExpr("contains", col, values[0]), nil
	case "Matches":
		// Partial-match semantics (DuckDB regexp_matches); anchor with ^…$
		// for a full match. DuckDB's ~ operator is a full match and must
		// not be used here.
		return funcExpr("regexp_matches", col, values[0]), nil
	}
	return nil, fmt.Errorf("%w: unsupported predicate op %q", ErrUnsupportedSyntax, op)
}

// funcExpr renders a plain function call. String operators render as
// DuckDB functions rather than LIKE patterns so parameter values stay
// literal — no pattern-metacharacter escaping anywhere.
func funcExpr(name string, args ...*pg.Node) *pg.Node {
	return pg.MakeFuncCallNode([]*pg.Node{pg.MakeStrNode(name)}, args, -1)
}

func opExpr(l *pg.Node, op string, r *pg.Node) *pg.Node {
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  pg.A_Expr_Kind_AEXPR_OP,
		Name:  []*pg.Node{pg.MakeStrNode(op)},
		Lexpr: l,
		Rexpr: r,
	}}}
}

func inExpr(col *pg.Node, values []*pg.Node, notIn bool) *pg.Node {
	// IN / NOT IN deparses from a List rexpr.
	listNode := &pg.Node{Node: &pg.Node_List{List: &pg.List{Items: values}}}
	name := "="
	kind := pg.A_Expr_Kind_AEXPR_IN
	if notIn {
		name = "<>"
	}
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  kind,
		Name:  []*pg.Node{pg.MakeStrNode(name)},
		Lexpr: col,
		Rexpr: listNode,
	}}}
}

func betweenExpr(col, lo, hi *pg.Node, notBetween bool) *pg.Node {
	kind := pg.A_Expr_Kind_AEXPR_BETWEEN
	if notBetween {
		kind = pg.A_Expr_Kind_AEXPR_NOT_BETWEEN
	}
	list := &pg.Node{Node: &pg.Node_List{List: &pg.List{Items: []*pg.Node{lo, hi}}}}
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  kind,
		Name:  []*pg.Node{pg.MakeStrNode("BETWEEN")},
		Lexpr: col,
		Rexpr: list,
	}}}
}

// columnRefFromString returns a ColumnRef with one segment (unqualified).
// Callers that need a qualified reference (e.g. t.col) should split on "."
// before calling.
func columnRefFromString(name string) *pg.Node {
	return &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: &pg.ColumnRef{
		Fields: []*pg.Node{pg.MakeStrNode(name)},
	}}}
}

func paramRef(n int) *pg.Node {
	return &pg.Node{Node: &pg.Node_ParamRef{ParamRef: &pg.ParamRef{Number: int32(n)}}}
}
