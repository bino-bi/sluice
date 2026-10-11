// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// checkAliasKeywords refuses a FROM alias that DuckDB reads as a keyword
// (parser.IsUnsafeBareAlias). pg_query reads such a word as an alias and
// deparses it bare. DuckDB then reads the keyword: a join keyword changes
// the join so the row filter and mask no longer apply, and qualify(x)
// becomes QUALIFY (x), a row predicate on the raw column x. It walks every
// pg.Alias (tables, subqueries, joins and functions carry one; select-list
// column aliases are plain strings, not Alias nodes, so they are not
// affected).
func checkAliasKeywords(raw *pg.ParseResult) error {
	if raw == nil {
		return nil
	}
	var found bool
	_ = walkMessages(raw.ProtoReflect(), func(m protoreflect.Message) bool {
		if a, ok := m.Interface().(*pg.Alias); ok {
			if parser.IsUnsafeBareAlias(a.GetAliasname()) {
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
