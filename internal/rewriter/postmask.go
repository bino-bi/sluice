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
	if s.mergedJoinReadsPostMask(raw) {
		return ErrMaskPostQueryContext
	}

	aliases := aliasMap{}
	for _, f := range sel.FromClause {
		s.recordFromItemForMask(f, aliases)
	}

	// Top-level target list: a bare masked column is recorded; a masked
	// column nested in an expression is refused.
	positions := map[int32]bool{}
	boundNames := map[string]bool{}
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
					name := rt.ResTarget.Name
					if name != "" {
						boundNames[foldName(name)] = true
					} else {
						name = column
					}
					positions[int32(i+1)] = true
					s.postMasks = append(s.postMasks, PostMask{
						ColumnIndex: i,
						OutputName:  name,
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
	if len(s.postMasks) > 0 {
		s.resultColumns = len(sel.TargetList)
		if s.boundItemReadRaw(sel, positions, boundNames) {
			return ErrMaskPostQueryContext
		}
		// unnest can add result columns and shift the bound indexes.
		for _, t := range sel.TargetList {
			if callsFunction(t, "unnest") {
				return ErrMaskPostQueryContext
			}
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
		if s.joinQualsUsePostQueryMask(f, aliases) {
			return ErrMaskPostQueryContext
		}
	}
	return nil
}

// joinQualsUsePostQueryMask checks the condition of a join and of every
// join nested in it. The mask pass leaves them to this planner.
func (s *state) joinQualsUsePostQueryMask(n *pg.Node, aliases aliasMap) bool {
	j := n.GetJoinExpr()
	if j == nil {
		return false
	}
	return s.exprUsesPostQueryMask(j.Quals, aliases) ||
		s.joinQualsUsePostQueryMask(j.Larg, aliases) ||
		s.joinQualsUsePostQueryMask(j.Rarg, aliases)
}

// boundItemReadRaw reports whether DuckDB reads a bound post-masked item
// on its raw value somewhere else in sel: by its select alias (DuckDB
// resolves it in every clause and in nested SELECTs), by its position in
// ORDER BY, GROUP BY or DISTINCT ON, or through SELECT DISTINCT.
// positions are the 1-based select list positions of the bound items.
func (s *state) boundItemReadRaw(sel *pg.SelectStmt, positions map[int32]bool, names map[string]bool) bool {
	for _, sb := range sel.SortClause {
		if positionIn(sb.GetSortBy().GetNode(), positions) {
			return true
		}
	}
	for _, n := range sel.GroupClause {
		if positionIn(n, positions) {
			return true
		}
	}
	for _, n := range sel.DistinctClause {
		// Plain SELECT DISTINCT holds one empty node.
		if n.GetNode() == nil || positionIn(n, positions) {
			return true
		}
	}

	// The FROM clause cannot see select aliases.
	skip := map[*pg.Node]bool{}
	for _, f := range sel.FromClause {
		skip[f] = true
	}
	for p := range positions {
		skip[sel.TargetList[p-1]] = true
	}
	found := false
	_ = walkTree(sel.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.Node:
			return !skip[v] && !found, nil
		case *pg.RangeSubselect:
			// A row-filter wrapper, also in a nested subquery, holds policy
			// SQL only. Its names must not decide a refusal.
			_, wrapper := s.wrappers[v]
			return !wrapper && !found, nil
		case *pg.WithClause:
			// A CTE body of sel cannot see its select aliases. A nested WITH
			// can, so only this one is skipped.
			return v != sel.WithClause && !found, nil
		case *pg.ColumnRef:
			if len(v.Fields) == 1 && names[foldName(v.Fields[0].GetString_().GetSval())] {
				found = true
			}
			return false, nil
		}
		return !found, nil
	})
	return found
}

// positionIn reports whether n is an integer constant, or a grouping set or
// row of them, that names one of positions. DuckDB reads such a constant
// in ORDER BY, GROUP BY and DISTINCT ON as a select list position, also
// under COLLATE.
func positionIn(n *pg.Node, positions map[int32]bool) bool {
	switch v := n.GetNode().(type) {
	case *pg.Node_AConst:
		return v.AConst.GetIval() != nil && positions[v.AConst.GetIval().GetIval()]
	case *pg.Node_CollateClause:
		return positionIn(v.CollateClause.GetArg(), positions)
	case *pg.Node_GroupingSet:
		for _, c := range v.GroupingSet.GetContent() {
			if positionIn(c, positions) {
				return true
			}
		}
	case *pg.Node_RowExpr:
		for _, a := range v.RowExpr.GetArgs() {
			if positionIn(a, positions) {
				return true
			}
		}
	}
	return false
}

// callsFunction reports whether a call to the named function appears below
// n. It stops at a nested SELECT, which returns one column to n.
func callsFunction(n *pg.Node, name string) bool {
	found := false
	_ = walkTree(n.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.SelectStmt:
			return false, nil
		case *pg.FuncCall:
			if names := v.GetFuncname(); len(names) > 0 && foldName(names[len(names)-1].GetString_().GetSval()) == name {
				found = true
			}
		}
		return !found, nil
	})
	return found
}

// mergedJoinReadsPostMask reports a USING or NATURAL join anywhere in the
// statement where a side reads a table with a post-query mask. The join
// compares the raw values, and USING names are not ColumnRefs, so the
// column walks cannot see them.
func (s *state) mergedJoinReadsPostMask(raw *pg.ParseResult) bool {
	found := false
	_ = walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		if j, ok := m.Interface().(*pg.JoinExpr); ok && (j.IsNatural || len(j.UsingClause) > 0) {
			if s.sideHasPostMask(j.Larg) || s.sideHasPostMask(j.Rarg) {
				found = true
			}
		}
		return !found, nil
	})
	return found
}

// sideHasPostMask reports whether a join side is a table with a post-query
// mask, its row-filter wrapper, or a join that holds one. A subquery the
// caller wrote refuses the masked column in its own SELECT.
func (s *state) sideHasPostMask(n *pg.Node) bool {
	switch v := n.GetNode().(type) {
	case *pg.Node_RangeVar:
		return s.tableHasPostMask(tableKeyOf(v.RangeVar, s.defaultCatalog))
	case *pg.Node_RangeSubselect:
		rv, ok := s.wrappers[v.RangeSubselect]
		return ok && s.tableHasPostMask(tableKeyOf(rv, s.defaultCatalog))
	case *pg.Node_JoinExpr:
		return s.sideHasPostMask(v.JoinExpr.GetLarg()) || s.sideHasPostMask(v.JoinExpr.GetRarg())
	}
	return false
}

func (s *state) tableHasPostMask(tableKey string) bool {
	for _, m := range s.decision.ColumnMasks {
		if m.TableKey == tableKey && s.isPostQueryMask(m) {
			return true
		}
	}
	return false
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
