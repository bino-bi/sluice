// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// walkTree visits m and every message below it, parents first. visit
// returns false to skip the children of the message it was given.
func walkTree(m protoreflect.Message, visit func(protoreflect.Message) (descend bool, err error)) error {
	descend, err := visit(m)
	if err != nil || !descend {
		return err
	}
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len() && err == nil; i++ {
				err = walkTree(list.Get(i).Message(), visit)
			}
			return err == nil
		}
		err = walkTree(val.Message(), visit)
		return err == nil
	})
	return err
}

// eachSubquery calls fn for every SelectStmt at or below m. It does not
// descend into a SelectStmt it has reported; fn owns that subtree.
func eachSubquery(m protoreflect.Message, fn func(*pg.SelectStmt) error) error {
	return walkTree(m, func(m protoreflect.Message) (bool, error) {
		if sel, ok := m.Interface().(*pg.SelectStmt); ok {
			return false, fn(sel)
		}
		return true, nil
	})
}

// eachNodeSubquery is eachSubquery for an optional node.
func eachNodeSubquery(n *pg.Node, fn func(*pg.SelectStmt) error) error {
	if n == nil {
		return nil
	}
	return eachSubquery(n.ProtoReflect(), fn)
}

// eachExprSubquery calls fn for the subqueries in the expression clauses
// of sel: the select list, WHERE, GROUP BY, HAVING, window definitions,
// ORDER BY, LIMIT, OFFSET and VALUES rows. WITH, FROM and the
// set-operation arms are skipped because every pass walks those itself.
// The clauses are found by reflection, so a clause cannot be forgotten.
func eachExprSubquery(sel *pg.SelectStmt, fn func(*pg.SelectStmt) error) error {
	var err error
	sel.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		switch fd.Name() {
		case "with_clause", "from_clause", "larg", "rarg":
			return true
		}
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len() && err == nil; i++ {
				err = eachSubquery(list.Get(i).Message(), fn)
			}
			return err == nil
		}
		err = eachSubquery(val.Message(), fn)
		return err == nil
	})
	return err
}
