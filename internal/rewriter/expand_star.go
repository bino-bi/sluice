// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"fmt"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/schema"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
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
	// EXISTS returns no columns. `EXISTS (SELECT * ...)` becomes
	// `EXISTS (SELECT 1 ...)`, so the star needs neither the schema cache
	// nor a mask.
	_ = walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		if sl, ok := m.Interface().(*pg.SubLink); ok && sl.SubLinkType == pg.SubLinkType_EXISTS_SUBLINK {
			if sel := sl.Subselect.GetSelectStmt(); sel != nil && sel.Larg == nil && hasStarTarget(sel.TargetList) {
				one := &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{Val: &pg.A_Const_Ival{Ival: &pg.Integer{Ival: 1}}}}}
				sel.TargetList = []*pg.Node{{Node: &pg.Node_ResTarget{ResTarget: &pg.ResTarget{Val: one}}}}
			}
		}
		return true, nil
	})
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
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt:
		if v.SelectStmt != nil {
			if err := s.expandStarInSelect(v.SelectStmt); err != nil {
				return err
			}
		}
	case *pg.Node_ExplainStmt:
		// Rewrite refuses EXPLAIN when a filter or mask applies. This arm
		// is a backstop in case that gate changes.
		if v.ExplainStmt != nil {
			if err := s.expandStarInNode(v.ExplainStmt.Query); err != nil {
				return err
			}
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
	// A star left in a nested SELECT over a masked table would hand the
	// raw column to the enclosing query under a name the mask pass cannot
	// follow, so nested SELECTs are expanded wherever they sit.
	for _, from := range sel.FromClause {
		if err := eachNodeSubquery(from, s.expandStarInSelect); err != nil {
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
	if err := eachExprSubquery(sel, s.expandStarInSelect); err != nil {
		return err
	}

	// Now consider the TargetList of *this* SELECT.
	if !hasStarTarget(sel.TargetList) {
		return nil
	}

	// Build the alias map for this select's FROM so t.* expansion can
	// resolve the qualifier.
	aliases := aliasMap{}
	var entries []fromEntry
	for _, f := range sel.FromClause {
		entries = appendFromEntries(entries, f, aliases, s.defaultCatalog)
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
	// A star over `a RIGHT JOIN b USING (k)` returns k once, taken from
	// whichever side has the row. Listing a.k and b.k instead would return
	// NULL keys for unmatched rows.
	if hasBareStar(sel.TargetList) && hasOuterMergedJoin(sel.FromClause) {
		return pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage(
			"SELECT * over a RIGHT or FULL join with USING or NATURAL is not supported on a filtered or masked table, list the columns")
	}

	newTargets := make([]*pg.Node, 0, len(sel.TargetList))
	for _, t := range sel.TargetList {
		rt, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || rt.ResTarget == nil {
			newTargets = append(newTargets, t)
			continue
		}
		_, qualifier, isStar := detectStar(rt.ResTarget)
		if !isStar {
			newTargets = append(newTargets, t)
			continue
		}
		expanded, err := s.expandOneStar(aliases, entries, qualifier)
		if err != nil {
			return err
		}
		if len(expanded) == 0 {
			// A table has columns. An empty list means the schema cache is
			// wrong, and keeping the star would skip the masks.
			return fmt.Errorf("%w: no columns known for the star", ErrSchemaMissing)
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
		if s.needsExpansion(ref.Catalog + "." + ref.Schema + "." + ref.Table) {
			return true
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

// fromEntry is one FROM item as star expansion sees it. A table gets its
// columns from the schema cache. Any other item (subquery, function) is
// opaque: its columns are whatever it produces, and a subquery has
// already been expanded and masked on its own.
type fromEntry struct {
	alias string // lower case, usable as a qualifier
	table bool
	// sub is set for a subquery without alias. It gets one when a star
	// has to name it.
	sub *pg.RangeSubselect
}

// appendFromEntries lists the items of one FROM entry in FROM order and
// registers its tables in aliases.
func appendFromEntries(entries []fromEntry, n *pg.Node, aliases aliasMap, defaultCatalog string) []fromEntry {
	if n == nil {
		return entries
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		if v.RangeVar == nil {
			return entries
		}
		if _, dup := aliases[aliasKey(v.RangeVar)]; dup {
			return entries
		}
		return append(entries, fromEntry{alias: recordTable(v.RangeVar, aliases, defaultCatalog), table: true})
	case *pg.Node_JoinExpr:
		if v.JoinExpr == nil {
			return entries
		}
		entries = appendFromEntries(entries, v.JoinExpr.Larg, aliases, defaultCatalog)
		return appendFromEntries(entries, v.JoinExpr.Rarg, aliases, defaultCatalog)
	case *pg.Node_RangeSubselect:
		alias := foldName(v.RangeSubselect.GetAlias().GetAliasname())
		if alias == "" {
			return append(entries, fromEntry{sub: v.RangeSubselect})
		}
		return append(entries, fromEntry{alias: alias})
	case *pg.Node_RangeFunction:
		alias := foldName(v.RangeFunction.GetAlias().GetAliasname())
		if alias == "" {
			// DuckDB names the output of an unaliased table function
			// after the function.
			alias = tableFunctionName(v.RangeFunction)
		}
		return append(entries, fromEntry{alias: alias})
	}
	return append(entries, fromEntry{})
}

// tableFunctionName returns the lower-case name of the first function of
// a FROM function item, or "".
func tableFunctionName(rf *pg.RangeFunction) string {
	if len(rf.GetFunctions()) != 1 {
		return ""
	}
	items := rf.Functions[0].GetList().GetItems()
	if len(items) == 0 {
		return ""
	}
	names := items[0].GetFuncCall().GetFuncname()
	if len(names) == 0 {
		return ""
	}
	return strings.ToLower(names[len(names)-1].GetString_().GetSval())
}

// hasBareStar reports whether targets holds an unqualified `*`.
func hasBareStar(targets []*pg.Node) bool {
	for _, t := range targets {
		if _, qualifier, ok := detectStar(t.GetResTarget()); ok && qualifier == "" {
			return true
		}
	}
	return false
}

// hasOuterMergedJoin reports whether a FROM list holds a RIGHT or FULL
// join that merges columns (USING or NATURAL).
func hasOuterMergedJoin(from []*pg.Node) bool {
	for _, n := range from {
		j := n.GetJoinExpr()
		if j == nil {
			continue
		}
		outer := j.Jointype == pg.JoinType_JOIN_RIGHT || j.Jointype == pg.JoinType_JOIN_FULL
		if outer && (j.IsNatural || len(j.UsingClause) > 0) {
			return true
		}
		if hasOuterMergedJoin([]*pg.Node{j.Larg, j.Rarg}) {
			return true
		}
	}
	return false
}

// expandOneStar returns the targets that replace one `*` or `t.*`. A
// table with a row filter or a mask becomes its column list. Every other
// item keeps a star of its own (`q.*`): a subquery is already expanded and
// masked, a function or an unprotected table has nothing to mask, and a
// CTE reference is not in the schema cache at all. An unqualified star
// covers every FROM item, in FROM order.
func (s *state) expandOneStar(aliases aliasMap, entries []fromEntry, qualifier string) ([]*pg.Node, error) {
	if qualifier != "" {
		qualifier = foldName(qualifier)
		for i := range entries {
			if entries[i].alias == qualifier {
				return s.starTargets(aliases, &entries[i])
			}
		}
		return nil, fmt.Errorf("%w: unknown qualifier %q", ErrUnsupportedSyntax, qualifier)
	}

	var out []*pg.Node
	for i := range entries {
		targets, err := s.starTargets(aliases, &entries[i])
		if err != nil {
			return nil, err
		}
		out = append(out, targets...)
	}
	return out, nil
}

// starTargets returns the targets one FROM item contributes to a star.
func (s *state) starTargets(aliases aliasMap, e *fromEntry) ([]*pg.Node, error) {
	if e.table {
		if ref := aliases[e.alias]; s.needsExpansion(ref.Catalog + "." + ref.Schema + "." + ref.Table) {
			return s.tableColumns(aliases, e.alias)
		}
		// Post-query masks bind to result column positions, so no star may
		// stay. A CTE reference is not in the schema cache and keeps it.
		if s.hasPostQueryMasks() {
			if cols, err := s.tableColumns(aliases, e.alias); err == nil && len(cols) > 0 {
				return cols, nil
			}
		}
		return []*pg.Node{newResTargetStar(e.alias)}, nil
	}
	if e.alias == "" && e.sub != nil {
		// Nothing can refer to an unaliased subquery by name, so giving
		// it one changes no meaning.
		s.subAliases++
		e.alias = fmt.Sprintf("sluice_sub%d", s.subAliases)
		e.sub.Alias = &pg.Alias{Aliasname: e.alias}
	}
	if e.alias == "" {
		return nil, fmt.Errorf("%w: SELECT * over a FROM item without a name", ErrUnsupportedSyntax)
	}
	return []*pg.Node{newResTargetStar(e.alias)}, nil
}

// needsExpansion reports whether a table has a row filter or a mask.
func (s *state) needsExpansion(tableKey string) bool {
	if _, ok := s.decision.RowFilters[tableKey]; ok {
		return true
	}
	return s.tableHasMask(tableKey)
}

// tableColumns returns one `alias.column` target per column of the table
// registered under alias.
func (s *state) tableColumns(aliases aliasMap, alias string) ([]*pg.Node, error) {
	ref := aliases[alias]
	key := schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}
	entry, err := s.schemaEntry(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSchemaMissing, key, err)
	}
	out := make([]*pg.Node, 0, len(entry.Columns))
	for _, col := range entry.Columns {
		out = append(out, newResTargetColumn(alias, col.Name))
	}
	return out, nil
}

// newResTargetStar returns a `SELECT qualifier.*` ResTarget node.
func newResTargetStar(qualifier string) *pg.Node {
	cr := &pg.ColumnRef{Fields: []*pg.Node{
		pg.MakeStrNode(qualifier),
		{Node: &pg.Node_AStar{AStar: &pg.A_Star{}}},
	}}
	rt := &pg.ResTarget{Val: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: cr}}}
	return &pg.Node{Node: &pg.Node_ResTarget{ResTarget: rt}}
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
