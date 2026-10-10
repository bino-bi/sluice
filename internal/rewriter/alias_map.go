// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// aliasMap resolves an identifier (alias name or table name) used as a
// qualifier in a ColumnRef back to a fully-qualified TableRef.
type aliasMap map[string]parser.TableRef

// recordTable registers rv under its alias, or under its table name when
// it has none, and returns that key. Keys and names are lower case: DuckDB
// resolves identifiers without regard to case, also when they are quoted,
// while pg_query keeps a quoted name as written.
func recordTable(rv *pg.RangeVar, out aliasMap, defaultCatalog string) string {
	ref := parser.TableRef{
		Catalog: strings.ToLower(rv.Catalogname),
		Schema:  strings.ToLower(rv.Schemaname),
		Table:   strings.ToLower(rv.Relname),
	}
	if ref.Catalog == "" {
		ref.Catalog = strings.ToLower(defaultCatalog)
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
		return foldName(rv.Alias.Aliasname)
	}
	return foldName(rv.Relname)
}

// foldName lower-cases ASCII letters only, like DuckDB does when it
// compares identifiers. "Ü" and "ü" stay two names.
func foldName(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// tableKeyOf is the policy decision key of rv.
func tableKeyOf(rv *pg.RangeVar, defaultCatalog string) string {
	catalog := rv.Catalogname
	if catalog == "" {
		catalog = defaultCatalog
	}
	return strings.ToLower(catalog + "." + rv.Schemaname + "." + rv.Relname)
}
