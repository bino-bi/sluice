// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// aliasMap resolves an identifier (alias name or table name) used as a
// qualifier in a ColumnRef back to a fully-qualified TableRef.
type aliasMap map[string]parser.TableRef

// recordTable registers rv under its alias, or under its table name when
// it has none, and returns that key. Keys and names are folded with
// parser.FoldASCII: DuckDB compares identifiers without regard to ASCII
// case, also when they are quoted, while pg_query keeps a quoted name as
// written.
func recordTable(rv *pg.RangeVar, out aliasMap, defaultCatalog string) string {
	ref := parser.TableRef{
		Catalog: parser.FoldASCII(rv.Catalogname),
		Schema:  parser.FoldASCII(rv.Schemaname),
		Table:   parser.FoldASCII(rv.Relname),
	}
	if ref.Catalog == "" {
		ref.Catalog = parser.FoldASCII(defaultCatalog)
	}
	key := aliasKey(rv)
	if rv.Alias != nil && rv.Alias.Aliasname != "" {
		ref.Alias = key
	}
	out[key] = ref
	return key
}

// aliasKey is the lower-case name a query uses to qualify columns of rv.
func aliasKey(rv *pg.RangeVar) string {
	if rv.Alias != nil && rv.Alias.Aliasname != "" {
		return parser.FoldASCII(rv.Alias.Aliasname)
	}
	return parser.FoldASCII(rv.Relname)
}

// tableKeyOf is the policy decision key of rv.
func tableKeyOf(rv *pg.RangeVar, defaultCatalog string) string {
	catalog := rv.Catalogname
	if catalog == "" {
		catalog = defaultCatalog
	}
	return parser.FoldASCII(catalog + "." + rv.Schemaname + "." + rv.Relname)
}
