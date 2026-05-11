// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// extractTables walks raw and returns every referenced table plus the
// distinct non-empty Catalog values (in order of first occurrence). CTE
// names are tracked in a per-scope stack so references to them are not
// emitted as TableRefs (concept §4.2).
func extractTables(raw *pg.RawStmt) ([]parser.TableRef, []string) {
	if raw == nil || raw.Stmt == nil {
		return nil, nil
	}
	w := &tableWalker{
		seen:    make(map[parser.TableRef]struct{}),
		ctes:    nil,
		aliases: make(map[string]struct{}),
	}
	w.walkNode(raw.Stmt)

	if len(w.tables) == 0 {
		return nil, nil
	}

	// Catalogs, preserving insertion order.
	var cats []string
	catSeen := make(map[string]struct{})
	for _, t := range w.tables {
		if t.Catalog == "" {
			continue
		}
		if _, dup := catSeen[t.Catalog]; dup {
			continue
		}
		catSeen[t.Catalog] = struct{}{}
		cats = append(cats, t.Catalog)
	}
	return w.tables, cats
}

// tableWalker holds the traversal state. ctes is a stack of CTE name sets
// so deeply-nested WITH clauses shadow correctly: a reference at depth N
// matches a CTE defined at any depth ≤ N.
type tableWalker struct {
	tables  []parser.TableRef
	seen    map[parser.TableRef]struct{}
	ctes    []map[string]struct{}
	aliases map[string]struct{}
}

// pushCTEScope begins a new WITH scope and registers every name inside it.
// The scope must be popped by the caller after descending.
func (w *tableWalker) pushCTEScope(names []string) {
	scope := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n != "" {
			scope[n] = struct{}{}
		}
	}
	w.ctes = append(w.ctes, scope)
}

// popCTEScope ends the innermost WITH scope.
func (w *tableWalker) popCTEScope() {
	if n := len(w.ctes); n > 0 {
		w.ctes = w.ctes[:n-1]
	}
}

// isCTEName reports whether name matches any CTE currently in scope.
func (w *tableWalker) isCTEName(name string) bool {
	for _, s := range w.ctes {
		if _, ok := s[name]; ok {
			return true
		}
	}
	return false
}

// addRef appends a deduplicated TableRef.
func (w *tableWalker) addRef(ref parser.TableRef) {
	if ref.Table == "" {
		return
	}
	if w.isCTEName(ref.Table) && ref.Schema == "" && ref.Catalog == "" {
		return
	}
	if _, dup := w.seen[ref]; dup {
		return
	}
	w.seen[ref] = struct{}{}
	w.tables = append(w.tables, ref)
}

// walkNode dispatches on the Node oneof. Only the variants that can
// contain a RangeVar or embed a statement need explicit handling; the
// rest are leaves for this walker.
func (w *tableWalker) walkNode(n *pg.Node) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RawStmt:
		if v.RawStmt != nil {
			w.walkNode(v.RawStmt.Stmt)
		}
	case *pg.Node_SelectStmt:
		w.walkSelect(v.SelectStmt)
	case *pg.Node_InsertStmt:
		if v.InsertStmt != nil {
			w.walkRelation(v.InsertStmt.Relation, "")
			w.walkWith(v.InsertStmt.WithClause)
			w.walkNode(v.InsertStmt.SelectStmt)
		}
	case *pg.Node_UpdateStmt:
		if v.UpdateStmt != nil {
			w.walkRelation(v.UpdateStmt.Relation, "")
			w.walkWith(v.UpdateStmt.WithClause)
			w.walkNodes(v.UpdateStmt.FromClause)
			w.walkNode(v.UpdateStmt.WhereClause)
		}
	case *pg.Node_DeleteStmt:
		if v.DeleteStmt != nil {
			w.walkRelation(v.DeleteStmt.Relation, "")
			w.walkWith(v.DeleteStmt.WithClause)
			w.walkNodes(v.DeleteStmt.UsingClause)
			w.walkNode(v.DeleteStmt.WhereClause)
		}
	case *pg.Node_MergeStmt:
		if v.MergeStmt != nil {
			w.walkRelation(v.MergeStmt.Relation, "")
			w.walkWith(v.MergeStmt.WithClause)
			w.walkNode(v.MergeStmt.SourceRelation)
		}
	case *pg.Node_CopyStmt:
		if v.CopyStmt != nil {
			w.walkRelation(v.CopyStmt.Relation, "")
			w.walkNode(v.CopyStmt.Query)
		}
	case *pg.Node_ExplainStmt:
		if v.ExplainStmt != nil {
			w.walkNode(v.ExplainStmt.Query)
		}
	case *pg.Node_RangeVar:
		w.walkRelation(v.RangeVar, "")
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil {
			w.walkNode(v.RangeSubselect.Subquery)
		}
	case *pg.Node_JoinExpr:
		if v.JoinExpr != nil {
			w.walkNode(v.JoinExpr.Larg)
			w.walkNode(v.JoinExpr.Rarg)
			w.walkNode(v.JoinExpr.Quals)
		}
	case *pg.Node_FromExpr:
		if v.FromExpr != nil {
			w.walkNodes(v.FromExpr.Fromlist)
			w.walkNode(v.FromExpr.Quals)
		}
	case *pg.Node_SubLink:
		if v.SubLink != nil {
			w.walkNode(v.SubLink.Subselect)
		}
	case *pg.Node_CommonTableExpr:
		if v.CommonTableExpr != nil {
			w.walkNode(v.CommonTableExpr.Ctequery)
		}
	case *pg.Node_BoolExpr:
		if v.BoolExpr != nil {
			w.walkNodes(v.BoolExpr.Args)
		}
	case *pg.Node_AExpr:
		if v.AExpr != nil {
			w.walkNode(v.AExpr.Lexpr)
			w.walkNode(v.AExpr.Rexpr)
		}
	}
}

// walkNodes walks a slice of Node.
func (w *tableWalker) walkNodes(ns []*pg.Node) {
	for _, n := range ns {
		w.walkNode(n)
	}
}

// walkRelation emits a TableRef from rv. aliasOverride lets callers
// propagate an alias applied to a subquery expression (currently unused
// here but kept for future JOIN ON edge cases).
func (w *tableWalker) walkRelation(rv *pg.RangeVar, aliasOverride string) {
	if rv == nil {
		return
	}
	alias := aliasOverride
	if alias == "" && rv.Alias != nil {
		alias = rv.Alias.Aliasname
	}
	w.addRef(parser.TableRef{
		Catalog: rv.Catalogname,
		Schema:  rv.Schemaname,
		Table:   rv.Relname,
		Alias:   alias,
	})
}

// walkWith registers CTE names in a new scope, walks each CTE body, then
// leaves the scope open: the caller pops it after walking the enclosing
// statement so sibling clauses still see the names.
func (w *tableWalker) walkWith(with *pg.WithClause) {
	if with == nil {
		return
	}
	names := make([]string, 0, len(with.Ctes))
	for _, cte := range with.Ctes {
		if cte == nil {
			continue
		}
		if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
			names = append(names, c.CommonTableExpr.Ctename)
		}
	}
	w.pushCTEScope(names)
	for _, cte := range with.Ctes {
		w.walkNode(cte)
	}
	// Scope stays pushed for the remainder of the current statement body;
	// it is popped in walkSelect / walk* after the body is processed.
}

// walkSelect handles SelectStmt including set-operation trees and the
// WithClause. It manages CTE scope push/pop so sibling queries in a UNION
// still see CTEs declared in a leading branch's WITH.
func (w *tableWalker) walkSelect(s *pg.SelectStmt) {
	if s == nil {
		return
	}
	hadWith := s.WithClause != nil
	if hadWith {
		w.walkWith(s.WithClause)
	}
	// Set-operation: recurse into both arms when present.
	if s.Larg != nil || s.Rarg != nil {
		w.walkNode(wrapSelect(s.Larg))
		w.walkNode(wrapSelect(s.Rarg))
	}
	w.walkNodes(s.FromClause)
	w.walkNode(s.WhereClause)
	w.walkNodes(s.GroupClause)
	w.walkNode(s.HavingClause)
	w.walkNodes(s.TargetList)
	if hadWith {
		w.popCTEScope()
	}
}

// wrapSelect wraps a *SelectStmt in a *Node. The protobuf API returns
// Larg/Rarg as *SelectStmt rather than *Node, so we lift them back into
// the oneof to reuse walkNode.
func wrapSelect(s *pg.SelectStmt) *pg.Node {
	if s == nil {
		return nil
	}
	return &pg.Node{Node: &pg.Node_SelectStmt{SelectStmt: s}}
}
