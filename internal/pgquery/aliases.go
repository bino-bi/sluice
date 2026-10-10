// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// joinKeywordAliases are DuckDB keywords that act as a join-type prefix
// between a relation and JOIN. pg_query does not know them, so it reads
// such a word as a table alias and deparses it bare; DuckDB then reads it
// as the keyword, changes the join, and the row filter and mask the
// rewriter built on the aliased table no longer apply. Verified against
// duckdb_keywords() in internal/executor.
var joinKeywordAliases = map[string]struct{}{
	"semi": {}, "anti": {}, "asof": {}, "positional": {},
}

// checkAliasKeywords refuses a FROM alias whose folded name is a join
// keyword. It walks every pg.Alias (tables, subqueries, joins and
// functions carry one; select-list column aliases are plain strings, not
// Alias nodes, so they are not affected).
func checkAliasKeywords(raw *pg.ParseResult) error {
	if raw == nil {
		return nil
	}
	var found bool
	_ = walkMessages(raw.ProtoReflect(), func(m protoreflect.Message) bool {
		if a, ok := m.Interface().(*pg.Alias); ok {
			if _, bad := joinKeywordAliases[foldASCII(a.GetAliasname())]; bad {
				found = true
				return false
			}
		}
		return !found
	})
	if found {
		return parser.ErrAliasKeyword
	}
	return nil
}

// foldASCII lower-cases an ASCII identifier, matching how DuckDB folds an
// unquoted or quoted name for keyword recognition.
func foldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// walkMessages visits m and every message below it; visit returns false to
// stop the walk.
func walkMessages(m protoreflect.Message, visit func(protoreflect.Message) bool) bool {
	if !visit(m) {
		return false
	}
	cont := true
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				if !walkMessages(list.Get(i).Message(), visit) {
					cont = false
					return false
				}
			}
			return true
		}
		if !walkMessages(val.Message(), visit) {
			cont = false
			return false
		}
		return true
	})
	return cont
}
