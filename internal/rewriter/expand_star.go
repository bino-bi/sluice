// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"fmt"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/schema"
)

// applyExpandStar walks every SelectStmt in the parse tree and replaces
// each SELECT * (or t.*) entry with explicit ColumnRef nodes derived
// from schema.Cache. When the cache is not populated the star is left
// intact unless a mask or filter applies to the table — in which case
// we refuse with ErrSchemaMissing (the mask substitution pass needs
// explicit references to do its work).
func (s *state) applyExpandStar(raw *pg.ParseResult) error {
	if raw == nil || len(raw.Stmts) == 0 {
		return nil
	}
	for _, stmt := range raw.Stmts {
		if stmt == nil {
			continue
		}
		if err := s.expandStarInNode(stmt.Stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) expandStarInNode(n *pg.Node) error {
	if n == nil {
		return nil
	}
	if v, ok := n.Node.(*pg.Node_SelectStmt); ok && v.SelectStmt != nil {
		if err := s.expandStarInSelect(v.SelectStmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) expandStarInSelect(sel *pg.SelectStmt) error {
	if sel == nil {
		return nil
	}
	// Recurse into subqueries and CTE bodies first so nested SELECTs are
	// expanded before we touch the enclosing one.
	if sel.WithClause != nil {
		for _, cte := range sel.WithClause.Ctes {
			if cte == nil {
				continue
			}
			if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
				if err := s.expandStarInNode(c.CommonTableExpr.Ctequery); err != nil {
					return err
				}
			}
		}
	}
	for _, from := range sel.FromClause {
		if err := s.expandStarInNode(from); err != nil {
			return err
		}
	}
	if sel.Larg != nil {
		if err := s.expandStarInSelect(sel.Larg); err != nil {
			return err
		}
	}
	if sel.Rarg != nil {
		if err := s.expandStarInSelect(sel.Rarg); err != nil {
			return err
		}
	}

	// Now consider the TargetList of *this* SELECT.
	if !hasStarTarget(sel.TargetList) {
		return nil
	}

	// Build the alias map for this select's FROM so t.* expansion can
	// resolve the qualifier.
	aliases := aliasMap{}
	for _, f := range sel.FromClause {
		recordFromItem(f, aliases, s.defaultCatalog)
	}

	// Determine whether the query needs expansion at all. If no active
	// row filter / column mask touches any of the FROM tables, leave
	// the `*` alone — DuckDB will expand it at execution time.
	if !s.fromNeedsExpansion(aliases) {
		return nil
	}
	if s.schema == nil {
		return fmt.Errorf("%w: schema cache not configured", ErrSchemaMissing)
	}

	newTargets := make([]*pg.Node, 0, len(sel.TargetList))
	for _, t := range sel.TargetList {
		rt, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || rt.ResTarget == nil {
			newTargets = append(newTargets, t)
			continue
		}
		star, qualifier, isStar := detectStar(rt.ResTarget)
		if !isStar {
			newTargets = append(newTargets, t)
			continue
		}
		expanded, err := s.expandOneStar(aliases, qualifier)
		if err != nil {
			return err
		}
		if len(expanded) == 0 {
			// No columns known (empty table?). Keep the star so DuckDB errors clearly.
			_ = star
			newTargets = append(newTargets, t)
			continue
		}
		newTargets = append(newTargets, expanded...)
	}
	sel.TargetList = newTargets
	return nil
}

// fromNeedsExpansion reports whether any table in the FROM clause has an
// active row filter or column mask on the current decision.
func (s *state) fromNeedsExpansion(aliases aliasMap) bool {
	for _, ref := range aliases {
		tkey := ref.Catalog + "." + ref.Schema + "." + ref.Table
		if _, ok := s.decision.RowFilters[tkey]; ok {
			return true
		}
		for _, m := range s.decision.ColumnMasks {
			if m.TableKey == tkey {
				return true
			}
		}
	}
	return false
}

// hasStarTarget scans a TargetList for any ResTarget that is a bare `*`
// or `t.*`. Used as a cheap pre-check.
func hasStarTarget(targets []*pg.Node) bool {
	for _, t := range targets {
		rt, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || rt.ResTarget == nil {
			continue
		}
		if _, _, ok := detectStar(rt.ResTarget); ok {
			return true
		}
	}
	return false
}

// detectStar returns (star-node, qualifier, true) when rt is a `*` or
// `t.*` target. qualifier is empty for a bare star.
func detectStar(rt *pg.ResTarget) (*pg.Node, string, bool) {
	if rt == nil || rt.Val == nil {
		return nil, "", false
	}
	cr, ok := rt.Val.Node.(*pg.Node_ColumnRef)
	if !ok || cr.ColumnRef == nil || len(cr.ColumnRef.Fields) == 0 {
		return nil, "", false
	}
	fields := cr.ColumnRef.Fields
	last := fields[len(fields)-1]
	if _, isStar := last.Node.(*pg.Node_AStar); !isStar {
		return nil, "", false
	}
	// Collect the qualifier (everything before the final *).
	qualifier := ""
	for i, f := range fields[:len(fields)-1] {
		s, ok := f.Node.(*pg.Node_String_)
		if !ok || s.String_ == nil {
			continue
		}
		if i > 0 {
			qualifier += "."
		}
		qualifier += s.String_.Sval
	}
	return rt.Val, qualifier, true
}

// expandOneStar returns the ColumnRef list that replaces one `*` or
// `t.*` target. Qualified stars restrict to that alias; unqualified ones
// expand across every table in the FROM list, in FROM order.
func (s *state) expandOneStar(aliases aliasMap, qualifier string) ([]*pg.Node, error) {
	var targets []struct {
		alias string
		ref   schema.Key
	}
	if qualifier != "" {
		ref, ok := aliases[qualifier]
		if !ok {
			return nil, fmt.Errorf("%w: unknown qualifier %q", ErrUnsupportedSyntax, qualifier)
		}
		targets = append(targets, struct {
			alias string
			ref   schema.Key
		}{qualifier, schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}})
	} else {
		for alias, ref := range aliases {
			targets = append(targets, struct {
				alias string
				ref   schema.Key
			}{alias, schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}})
		}
	}

	var out []*pg.Node
	for _, t := range targets {
		entry, err := s.schema.Get(context.Background(), t.ref)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrSchemaMissing, t.ref, err)
		}
		for _, col := range entry.Columns {
			out = append(out, newResTargetColumn(t.alias, col.Name))
		}
	}
	return out, nil
}

// newResTargetColumn returns a `SELECT alias.column` ResTarget node.
func newResTargetColumn(qualifier, column string) *pg.Node {
	var fields []*pg.Node
	if qualifier != "" {
		fields = append(fields, pg.MakeStrNode(qualifier))
	}
	fields = append(fields, pg.MakeStrNode(column))
	cr := &pg.ColumnRef{Fields: fields}
	rt := &pg.ResTarget{
		Val: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: cr}},
	}
	return &pg.Node{Node: &pg.Node_ResTarget{ResTarget: rt}}
}
