// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

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
		s.recordFromItemForMask(f, aliases)
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
				m, tableKey, ok, err := s.lookupMaskForColumn(aliases, qualifier, column)
				if err != nil {
					return err
				}
				if ok && s.isPostQueryMask(m) {
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
	for _, list := range [][]*pg.Node{
		sel.SortClause, sel.DistinctClause, sel.WindowClause, sel.ValuesLists,
		{sel.LimitCount, sel.LimitOffset},
	} {
		for _, n := range list {
			if s.exprUsesPostQueryMask(n, aliases) {
				return ErrMaskPostQueryContext
			}
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

// exprUsesPostQueryMask reports whether any ColumnRef below n resolves
// (via aliases) to a post-query-masked column. The walk is by reflection
// so no node type (ARRAY[...], COLLATE, subscripts, ...) can hide one. It
// stops at a nested SELECT: the mask pass refuses those uses itself.
func (s *state) exprUsesPostQueryMask(n *pg.Node, aliases aliasMap) bool {
	if n == nil {
		return false
	}
	found := false
	_ = walkTree(n.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.SelectStmt:
			return false, nil
		case *pg.ColumnRef:
			qualifier, column := splitColumnRef(v)
			if column == "" {
				return false, nil
			}
			// An ambiguous reference counts as a use: refusing is the safe side.
			if m, _, ok, err := s.lookupMaskForColumn(aliases, qualifier, column); err != nil || (ok && s.isPostQueryMask(m)) {
				found = true
			}
			return false, nil
		}
		return !found, nil
	})
	return found
}
