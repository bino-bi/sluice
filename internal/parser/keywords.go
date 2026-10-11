// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// unsafeBareAliases are the DuckDB keywords that pg_query deparses bare
// and that DuckDB does not read as the alias of a subquery before JOIN.
// pg_query does not know the join-type prefixes semi, anti, asof and
// positional, reads such a word as an alias and deparses it bare. DuckDB
// then reads the keyword and changes the join. The other words make that
// statement fail to parse. With a column list, qualify(x) does parse, as a
// QUALIFY clause. TestWrapperAliasKeywords in internal/executor checks this
// list against duckdb_keywords().
var unsafeBareAliases = nameSet(`semi anti asof positional`, `at by describe
	glob lambda pivot pivot_longer pivot_wider qualify show summarize unpack
	unpivot`)

// IsUnsafeBareAlias reports whether DuckDB would not read name, as
// pg_query deparses it after a subquery, as its alias. The match is exact:
// pg_query quotes a name with upper case, and DuckDB never reads a quoted
// name as a keyword.
func IsUnsafeBareAlias(name string) bool {
	_, ok := unsafeBareAliases[name]
	return ok
}

// FoldASCII lower-cases A-Z only, like DuckDB does for keywords and names.
func FoldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
