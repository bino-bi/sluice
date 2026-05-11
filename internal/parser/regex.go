// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"regexp"
	"strings"
)

// tableRefRegex matches a FROM / JOIN / UPDATE / INTO clause followed by a
// dotted identifier. It is intentionally permissive: false positives are
// cheap (the caller treats the result as a lower bound on referenced
// tables) but false negatives let protected data escape.
//
// The expression matches up to three dotted segments with optional
// double-quoted identifiers. Trailing aliases are not consumed; the caller
// does not need them.
var tableRefRegex = regexp.MustCompile(
	`(?i)\b(?:from|join|update|into)\s+` +
		`("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)` +
		`(?:\s*\.\s*("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*))?` +
		`(?:\s*\.\s*("[^"]+"|[A-Za-z_][A-Za-z0-9_$]*))?`,
)

// ExtractTablesRegex returns table references using a tolerant regex over
// FROM/JOIN/UPDATE/INTO clauses. Used when Parse fails to decide whether
// rejecting or passing through is safe (concept §4.8).
//
// Results are always over-approximations: the caller treats them as "at
// least these tables are referenced". If any intersects a policy-protected
// table, the query is rejected; otherwise it is passed through.
//
// The scanner strips single-line (--…) and block (/*…*/) comments, and
// skips single-quoted string literals, to avoid false positives from
// comments or strings that happen to contain the word "FROM".
func ExtractTablesRegex(sql string) []TableRef {
	clean := stripCommentsAndStrings(sql)
	matches := tableRefRegex.FindAllStringSubmatch(clean, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[TableRef]struct{}, len(matches))
	out := make([]TableRef, 0, len(matches))
	for _, m := range matches {
		ref := normaliseDottedMatch(m[1], m[2], m[3])
		if ref.Table == "" {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

// normaliseDottedMatch turns up to three dotted identifier fragments into a
// TableRef. Missing segments leave Catalog/Schema empty — the resolver
// fills in defaults later.
func normaliseDottedMatch(a, b, c string) TableRef {
	a = unquoteIdent(a)
	b = unquoteIdent(b)
	c = unquoteIdent(c)

	switch {
	case c != "":
		return TableRef{Catalog: a, Schema: b, Table: c}
	case b != "":
		return TableRef{Schema: a, Table: b}
	default:
		return TableRef{Table: a}
	}
}

// unquoteIdent strips surrounding double quotes from a SQL identifier.
// Identifiers without quotes are returned unchanged.
func unquoteIdent(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

// stripCommentsAndStrings replaces SQL comments and single-quoted strings
// with spaces so the regex does not match inside them. The replacement
// preserves offsets (useful if we later want to surface line numbers).
func stripCommentsAndStrings(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))

	runes := []rune(sql)
	n := len(runes)
	for i := 0; i < n; i++ {
		ch := runes[i]
		// -- line comment
		if ch == '-' && i+1 < n && runes[i+1] == '-' {
			for i < n && runes[i] != '\n' {
				b.WriteRune(' ')
				i++
			}
			if i < n {
				b.WriteRune('\n')
			}
			continue
		}
		// /* block comment */
		if ch == '/' && i+1 < n && runes[i+1] == '*' {
			b.WriteRune(' ')
			b.WriteRune(' ')
			i += 2
			for i+1 < n && (runes[i] != '*' || runes[i+1] != '/') {
				if runes[i] == '\n' {
					b.WriteRune('\n')
				} else {
					b.WriteRune(' ')
				}
				i++
			}
			if i+1 < n {
				b.WriteRune(' ')
				b.WriteRune(' ')
				i++ // land on '/'; outer loop's i++ steps past it
			}
			continue
		}
		// single-quoted string; doubled quote '' is an embedded quote.
		if ch == '\'' {
			b.WriteRune(' ')
			i++
			for i < n {
				if runes[i] == '\'' {
					if i+1 < n && runes[i+1] == '\'' {
						b.WriteRune(' ')
						b.WriteRune(' ')
						i += 2
						continue
					}
					b.WriteRune(' ')
					break
				}
				if runes[i] == '\n' {
					b.WriteRune('\n')
				} else {
					b.WriteRune(' ')
				}
				i++
			}
			continue
		}
		b.WriteRune(ch)
	}
	return b.String()
}
