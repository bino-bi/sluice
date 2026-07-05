// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/policy"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// isPostQueryMask reports whether m must be applied after execution rather
// than as a SQL expression.
func (s *state) isPostQueryMask(m *policy.CompiledMask) bool {
	reg := s.masks
	if reg == nil {
		reg = pkgmask.Default()
	}
	p, ok := reg.Lookup(string(m.Type))
	if !ok {
		return false
	}
	return pkgmask.IsPostQuery(p, m.Args)
}

// hasPostQueryMasks reports whether any mask on the decision is post-query.
func (s *state) hasPostQueryMasks() bool {
	for _, m := range s.decision.ColumnMasks {
		if s.isPostQueryMask(m) {
			return true
		}
	}
	return false
}

// planPostMasks records post-query masks bound to bare top-level select
// items and refuses any other use of a post-query-masked column. It runs
// on the top-level statement only; post-query masks over set operations
// (UNION/INTERSECT/EXCEPT) are refused because result-column provenance is
// ambiguous.
func (s *state) planPostMasks(raw *pg.ParseResult) error {
	if len(raw.Stmts) != 1 {
		return ErrMaskPostQueryContext
	}
	sel := raw.Stmts[0].Stmt.GetSelectStmt()
	if sel == nil || sel.Larg != nil || sel.Rarg != nil {
		return ErrMaskPostQueryContext
	}
	// A residual star means the result-column count is unknown, so the
	// bound indexes cannot be trusted. Refuse fail-closed.
	if hasStarTarget(sel.TargetList) {
		return ErrMaskPostQueryContext
	}

	aliases := aliasMap{}
	for _, f := range sel.FromClause {
		recordFromItemForMask(f, aliases, s.defaultCatalog)
	}

	// Top-level target list: a bare masked column is recorded; a masked
	// column nested in an expression is refused.
	for i, t := range sel.TargetList {
		rt, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || rt.ResTarget == nil {
			continue
		}
		if cr, ok := rt.ResTarget.Val.Node.(*pg.Node_ColumnRef); ok {
			qualifier, column := splitColumnRef(cr.ColumnRef)
			if column != "" {
				if m, tableKey, ok := s.lookupMaskForColumn(aliases, qualifier, column); ok && s.isPostQueryMask(m) {
					s.postMasks = append(s.postMasks, PostMask{
						ColumnIndex: i,
						TableKey:    tableKey,
						Column:      column,
						Type:        m.Type,
						Args:        m.Args,
						Policy:      m.Policy,
					})
					s.rewrites = append(s.rewrites, "post-mask:"+tableKey+"."+column)
					continue
				}
			}
		}
		// Non-bare select item: any post-query-masked column inside is a leak.
		if s.exprUsesPostQueryMask(rt.ResTarget.Val, aliases) {
			return ErrMaskPostQueryContext
		}
	}

	// A post-query-masked column may not appear in any predicate or
	// grouping/ordering position — those read the raw value.
	for _, n := range append([]*pg.Node{sel.WhereClause, sel.HavingClause}, sel.GroupClause...) {
		if s.exprUsesPostQueryMask(n, aliases) {
			return ErrMaskPostQueryContext
		}
	}
	for _, o := range sel.SortClause {
		if s.exprUsesPostQueryMask(o, aliases) {
			return ErrMaskPostQueryContext
		}
	}
	for _, f := range sel.FromClause {
		if j, ok := f.Node.(*pg.Node_JoinExpr); ok && j.JoinExpr != nil {
			if s.exprUsesPostQueryMask(j.JoinExpr.Quals, aliases) {
				return ErrMaskPostQueryContext
			}
		}
	}
	return nil
}

// exprUsesPostQueryMask reports whether any ColumnRef in the expression
// tree resolves (via aliases) to a post-query-masked column.
func (s *state) exprUsesPostQueryMask(n *pg.Node, aliases aliasMap) bool {
	found := false
	walkColumnRefs(n, func(cr *pg.ColumnRef) {
		if found {
			return
		}
		qualifier, column := splitColumnRef(cr)
		if column == "" {
			return
		}
		if m, _, ok := s.lookupMaskForColumn(aliases, qualifier, column); ok && s.isPostQueryMask(m) {
			found = true
		}
	})
	return found
}

// walkColumnRefs invokes fn for every ColumnRef in the expression tree.
func walkColumnRefs(n *pg.Node, fn func(*pg.ColumnRef)) {
	if n == nil || n.Node == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_ColumnRef:
		fn(v.ColumnRef)
	case *pg.Node_AExpr:
		walkColumnRefs(v.AExpr.Lexpr, fn)
		walkColumnRefs(v.AExpr.Rexpr, fn)
	case *pg.Node_BoolExpr:
		for _, a := range v.BoolExpr.Args {
			walkColumnRefs(a, fn)
		}
	case *pg.Node_FuncCall:
		for _, a := range v.FuncCall.Args {
			walkColumnRefs(a, fn)
		}
	case *pg.Node_TypeCast:
		walkColumnRefs(v.TypeCast.Arg, fn)
	case *pg.Node_CaseExpr:
		walkColumnRefs(v.CaseExpr.Arg, fn)
		for _, w := range v.CaseExpr.Args {
			walkColumnRefs(w, fn)
		}
		walkColumnRefs(v.CaseExpr.Defresult, fn)
	case *pg.Node_CaseWhen:
		walkColumnRefs(v.CaseWhen.Expr, fn)
		walkColumnRefs(v.CaseWhen.Result, fn)
	case *pg.Node_CoalesceExpr:
		for _, a := range v.CoalesceExpr.Args {
			walkColumnRefs(a, fn)
		}
	case *pg.Node_MinMaxExpr:
		for _, a := range v.MinMaxExpr.Args {
			walkColumnRefs(a, fn)
		}
	case *pg.Node_NullTest:
		walkColumnRefs(v.NullTest.Arg, fn)
	case *pg.Node_BooleanTest:
		walkColumnRefs(v.BooleanTest.Arg, fn)
	case *pg.Node_SubLink:
		// Correlated column references inside a subquery read raw values;
		// surface them so the caller refuses.
		walkColumnRefs(v.SubLink.Testexpr, fn)
	case *pg.Node_List:
		for _, item := range v.List.Items {
			walkColumnRefs(item, fn)
		}
	}
}
