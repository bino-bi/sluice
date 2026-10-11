// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"fmt"
	"sort"
)

// CheckFunctions returns the reason a statement must be refused because of
// a function call or FROM item, or "" when there is none.
//
// Policy and the rewriter key on table references. A function can read
// other tables, files, settings or secrets without naming a table, so
// functions are default-deny: only names on the allowlists pass.
func CheckFunctions(s QueryShape) string {
	if len(s.UnsupportedFrom) > 0 {
		return fmt.Sprintf("%s in FROM is not supported", s.UnsupportedFrom[0])
	}
	for _, f := range s.Functions {
		// A qualifier could resolve to a macro stored in an attached
		// database, and DuckDB reads col.f() as f(col), which the
		// post-query mask checks do not see as a use of col. pg_query
		// itself writes LIKE ... ESCAPE and SIMILAR TO as pg_catalog calls.
		if !qualifierAllowed(f) {
			return fmt.Sprintf("qualified function name %q is not allowed, write f(x) instead of schema.f(x) or x.f()", clip(f.Qualifier+"."+f.Name))
		}
		if f.InFrom {
			if _, ok := tableFunctions[f.Name]; !ok {
				return fmt.Sprintf("table function %q is not allowed", f.Name)
			}
			// The post-query mask checks do not walk FROM function
			// arguments, so a post-masked column there, also in a
			// subquery, would be read raw.
			if f.DynamicArgs {
				return fmt.Sprintf("arguments of table function %q must not contain columns or subqueries", f.Name)
			}
			continue
		}
		if _, ok := exprFunctions[f.Name]; ok {
			continue
		}
		if _, ok := syntaxFunctions[f.Name]; ok {
			continue
		}
		return fmt.Sprintf("function %q is not allowed", f.Name)
	}
	return ""
}

// qualifierAllowed reports whether a function call's qualifier is
// acceptable: none, or the pg_catalog prefix pg_query synthesises for
// LIKE ... ESCAPE and SIMILAR TO.
func qualifierAllowed(f FuncRef) bool {
	if f.Qualifier == "" {
		return true
	}
	return f.Qualifier == "pg_catalog" && (f.Name == "like_escape" || f.Name == "similar_to_escape")
}

// clip bounds a caller-controlled name before it goes into the client
// message and the audit record. A qualifier can have any number of parts.
func clip(name string) string {
	const limit = 128
	if len(name) <= limit {
		return name
	}
	return name[:limit] + "..."
}

// AllowedTableFunctions returns the functions permitted in FROM, sorted.
func AllowedTableFunctions() []string { return sortedKeys(tableFunctions) }

// AllowedExprFunctions returns the catalog functions permitted outside
// FROM, sorted. Names produced by SQL syntax forms are not included.
func AllowedExprFunctions() []string { return sortedKeys(exprFunctions) }

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
