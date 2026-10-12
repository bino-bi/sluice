// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"errors"
	"fmt"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/schema"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// applyExpandStar replaces a SELECT * (or t.*) entry with explicit
// ColumnRef nodes derived from schema.Cache when the decision has a
// post-query mask. Those masks bind to result column positions, so the
// outer select list is always expanded. A star in a nested SELECT is
// expanded when its own FROM holds a table with a post-query mask: it
// would hand the masked column to its parent under a name the guard walk
// cannot follow. A table with a post-query mask must have a cache entry;
// other tables are expanded when the cache knows them. Every other star
// stays: DuckDB expands it over the table wrappers, which hold the masked
// values.
func (s *state) applyExpandStar(raw *pg.ParseResult) error {
	if raw == nil || len(raw.Stmts) == 0 || !s.hasPostQueryMasks() {
		return nil
	}
	s.topSelect = raw.Stmts[0].GetStmt().GetSelectStmt()
	// EXISTS returns no columns. `EXISTS (SELECT * ...)` becomes
	// `EXISTS (SELECT 1 ...)`, so the star needs neither the schema cache
	// nor a mask.
	_ = walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		if sl, ok := m.Interface().(*pg.SubLink); ok && sl.SubLinkType == pg.SubLinkType_EXISTS_SUBLINK {
			if sel := sl.Subselect.GetSelectStmt(); isLoneStarSelect(sel) {
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

// isLoneStarSelect reports whether the select list of sel is one star and
// nothing in sel depends on it. An item next to the star can change the
// row count (unnest) or be read by its alias. ORDER BY, GROUP BY and
// DISTINCT ON can name a select list position.
func isLoneStarSelect(sel *pg.SelectStmt) bool {
	if sel == nil || sel.Larg != nil || len(sel.TargetList) != 1 || !hasStarTarget(sel.TargetList) {
		return false
	}
	// Plain SELECT DISTINCT holds one empty node.
	distinctOn := len(sel.DistinctClause) > 0 && sel.DistinctClause[0].GetNode() != nil
	return len(sel.SortClause) == 0 && len(sel.GroupClause) == 0 && !distinctOn
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
	// A star left in a nested SELECT over a post-masked table would hand
	// the raw column to the enclosing query under a name the guard walk
	// cannot follow, so nested SELECTs are visited wherever they sit.
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
	// DuckDB expands a star over the FROM items of its own SELECT only, so
	// this one hands no post-masked column on.
	if sel != s.topSelect && !s.fromHasPostMask(aliases) {
		return nil
	}
	if s.schema == nil {
		return fmt.Errorf("%w: schema cache not configured", ErrSchemaMissing)
	}
	// A star over `a JOIN b USING (k)` returns k once, for an outer join
	// from the side that has the row. Listing a.k and b.k instead would
	// return it twice, with NULL keys for unmatched rows.
	if hasBareStar(sel.TargetList) && hasMergedJoin(sel.FromClause) {
		return pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage(
			"SELECT * over a join with USING or NATURAL is not supported with post-query masks, list the columns")
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
			// wrong, and a star kept here would keep a post-query masked
			// column out of the plan.
			return fmt.Errorf("%w: no columns known for the star", ErrSchemaMissing)
		}
		newTargets = append(newTargets, expanded...)
	}
	sel.TargetList = newTargets
	return nil
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
// already been expanded on its own.
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

// hasMergedJoin reports whether a FROM list holds a join that merges
// columns (USING or NATURAL).
func hasMergedJoin(from []*pg.Node) bool {
	for _, n := range from {
		j := n.GetJoinExpr()
		if j == nil {
			continue
		}
		if j.IsNatural || len(j.UsingClause) > 0 || hasMergedJoin([]*pg.Node{j.Larg, j.Rarg}) {
			return true
		}
	}
	return false
}

// fromHasPostMask reports whether one of the FROM tables in aliases has a
// post-query mask.
func (s *state) fromHasPostMask(aliases aliasMap) bool {
	for _, ref := range aliases {
		if s.tableHasPostMask(ref.Catalog + "." + ref.Schema + "." + ref.Table) {
			return true
		}
	}
	return false
}

// expandOneStar returns the targets that replace one `*` or `t.*`. A
// table becomes its column list. Every other item keeps a star of its own
// (`q.*`): a subquery is already expanded, a function has nothing to mask,
// and a CTE reference is not in the schema cache at all. An unqualified
// star covers every FROM item, in FROM order.
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
		if ref := aliases[e.alias]; s.tableHasPostMask(ref.Catalog + "." + ref.Schema + "." + ref.Table) {
			return s.tableColumns(aliases, e.alias)
		}
		// Post-query masks bind to result column positions, so no star may
		// stay. A CTE reference is not in the schema cache and keeps it. A
		// load that failed is not such a miss and keeps its error.
		cols, err := s.tableColumns(aliases, e.alias)
		switch {
		case err == nil && len(cols) > 0:
			return cols, nil
		case err != nil && !errors.Is(err, schema.ErrUnknownTable) && !errors.Is(err, schema.ErrUnknownCatalog):
			return nil, err
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

// tableColumns returns one `alias.column` target per column of the table
// registered under alias.
func (s *state) tableColumns(aliases aliasMap, alias string) ([]*pg.Node, error) {
	ref := aliases[alias]
	entry, err := s.tableEntry(ref.Catalog + "." + ref.Schema + "." + ref.Table)
	if err != nil {
		return nil, err
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
