// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// aliasMap resolves an identifier (alias name or table name) used as a
// qualifier in a ColumnRef back to a fully-qualified TableRef. It is
// populated in one pre-pass over the SELECT's FromClause and subquery
// bodies.
type aliasMap map[string]parser.TableRef

// walkFromForAliases walks the FROM tree of every SelectStmt in n and
// records (alias-or-relname) → TableRef into out. Unqualified tables
// use relname as the key; aliased tables use the alias name.
func walkFromForAliases(n *pg.Node, out aliasMap, defaultCatalog string) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RawStmt:
		if v.RawStmt != nil {
			walkFromForAliases(v.RawStmt.Stmt, out, defaultCatalog)
		}
	case *pg.Node_SelectStmt:
		if v.SelectStmt == nil {
			return
		}
		for _, f := range v.SelectStmt.FromClause {
			recordFromItem(f, out, defaultCatalog)
		}
	}
}

func recordFromItem(n *pg.Node, out aliasMap, defaultCatalog string) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		if v.RangeVar == nil {
			return
		}
		ref := parser.TableRef{
			Catalog: v.RangeVar.Catalogname,
			Schema:  v.RangeVar.Schemaname,
			Table:   v.RangeVar.Relname,
		}
		if ref.Catalog == "" {
			ref.Catalog = defaultCatalog
		}
		key := v.RangeVar.Relname
		if v.RangeVar.Alias != nil && v.RangeVar.Alias.Aliasname != "" {
			key = v.RangeVar.Alias.Aliasname
			ref.Alias = key
		}
		out[key] = ref
	case *pg.Node_JoinExpr:
		if v.JoinExpr == nil {
			return
		}
		recordFromItem(v.JoinExpr.Larg, out, defaultCatalog)
		recordFromItem(v.JoinExpr.Rarg, out, defaultCatalog)
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect == nil {
			return
		}
		walkFromForAliases(v.RangeSubselect.Subquery, out, defaultCatalog)
	}
}
