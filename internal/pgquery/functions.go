// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"fmt"
	"sort"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
)

// collectFunctions returns every function call in raw, at any depth, the
// FROM items that are not a table, subquery, join or function, and whether
// raw has a $n parameter reference. The walk is generic over the protobuf
// tree so no clause can hide a call.
func collectFunctions(raw *pg.RawStmt) ([]parser.FuncRef, []string, bool) {
	if raw == nil || raw.Stmt == nil {
		return nil, nil, false
	}
	c := &funcCollector{
		seenFuncs: make(map[parser.FuncRef]struct{}),
		seenFrom:  make(map[string]struct{}),
	}
	c.walkNode(raw.Stmt)
	// protoreflect leaves the field iteration order undefined. Sort so the
	// reject reason for a statement is stable: FROM calls first, then by name.
	sort.Slice(c.funcs, func(i, j int) bool {
		a, b := c.funcs[i], c.funcs[j]
		if a.InFrom != b.InFrom {
			return a.InFrom
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Qualifier != b.Qualifier {
			return a.Qualifier < b.Qualifier
		}
		return !a.DynamicArgs && b.DynamicArgs
	})
	sort.Strings(c.from)
	return c.funcs, c.from, c.params
}

type funcCollector struct {
	funcs     []parser.FuncRef
	seenFuncs map[parser.FuncRef]struct{}
	from      []string
	seenFrom  map[string]struct{}
	params    bool
}

func (c *funcCollector) addFunc(ref parser.FuncRef) {
	if _, dup := c.seenFuncs[ref]; dup {
		return
	}
	c.seenFuncs[ref] = struct{}{}
	c.funcs = append(c.funcs, ref)
}

func (c *funcCollector) addFrom(kind string) {
	if _, dup := c.seenFrom[kind]; dup {
		return
	}
	c.seenFrom[kind] = struct{}{}
	c.from = append(c.from, kind)
}

func (c *funcCollector) walkNode(n *pg.Node) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeFunction:
		// Calls directly under a RangeFunction are table functions; the
		// generic descent below would record them as expression calls.
		c.walkRangeFunction(v.RangeFunction)
		return
	case *pg.Node_FuncCall:
		c.addFunc(funcRef(v.FuncCall, false))
	case *pg.Node_SqlvalueFunction:
		c.addFunc(parser.FuncRef{Name: sqlValueFunctionName(v.SqlvalueFunction)})
	case *pg.Node_ParamRef:
		c.params = true
	default:
		// SQL/JSON, XML and MERGE syntax has its own node types. DuckDB
		// runs the deparsed text as ordinary calls, so name them.
		if name := keywordCallName(n); name != "" {
			c.addFunc(parser.FuncRef{Name: name})
		}
	}
	c.walkFields(n.ProtoReflect())
}

func (c *funcCollector) walkMessage(m protoreflect.Message) {
	switch v := m.Interface().(type) {
	case *pg.Node:
		c.walkNode(v)
		return
	case *pg.SelectStmt:
		// Matched on the message, not the Node wrapper: set-operation
		// arms are plain *SelectStmt fields.
		for _, f := range v.FromClause {
			c.checkFromItem(f)
		}
	case *pg.RangeVar:
		// DuckDB opens a table name that is not in the catalog as a file
		// or URL (replacement scan). That read would skip the row filters
		// and masks defined on the view over the same file.
		if isPathLike(v.Catalogname) || isPathLike(v.Schemaname) || isPathLike(v.Relname) {
			c.addFrom(`table name with path characters (/ \ : * ? [)`)
		}
	}
	c.walkFields(m)
}

func (c *funcCollector) walkFields(m protoreflect.Message) {
	eachChildMessage(m, func(child protoreflect.Message) bool {
		c.walkMessage(child)
		return true
	})
}

// checkFromItem records a FROM item of a kind the rewriter passes do not
// handle. Row filters and masks are only applied to tables, subqueries
// and joins.
func (c *funcCollector) checkFromItem(n *pg.Node) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar, *pg.Node_RangeSubselect, *pg.Node_RangeFunction:
	case *pg.Node_JoinExpr:
		if v.JoinExpr != nil {
			c.checkFromItem(v.JoinExpr.Larg)
			c.checkFromItem(v.JoinExpr.Rarg)
		}
	case *pg.Node_RangeTableSample:
		c.addFrom("TABLESAMPLE")
	case *pg.Node_RangeTableFunc:
		c.addFrom("XMLTABLE")
	case *pg.Node_JsonTable:
		c.addFrom("JSON_TABLE")
	default:
		c.addFrom(strings.TrimPrefix(fmt.Sprintf("%T", n.Node), "*pg_query.Node_"))
	}
}

// walkRangeFunction handles `FROM f(...)` and `FROM ROWS FROM (f(...), ...)`.
// Each entry of Functions is a List of [function expression, column
// definition list].
func (c *funcCollector) walkRangeFunction(rf *pg.RangeFunction) {
	if rf == nil {
		return
	}
	for _, fn := range rf.Functions {
		items := fn.GetList().GetItems()
		if len(items) == 0 {
			c.addFrom("function")
			continue
		}
		fc := items[0].GetFuncCall()
		if fc == nil {
			// The grammar also accepts keyword forms here (CURRENT_DATE,
			// CAST, COALESCE, ...). They are not plain calls.
			c.addFrom("non-function expression")
			c.walkNode(items[0])
		} else {
			ref := funcRef(fc, true)
			ref.DynamicArgs = hasColumnOrSubquery(fc.ProtoReflect())
			c.addFunc(ref)
			c.walkFields(fc.ProtoReflect())
		}
		for _, item := range items[1:] {
			c.walkNode(item)
		}
	}
	for _, n := range rf.Coldeflist {
		c.walkNode(n)
	}
}

// funcRef splits a FuncCall name into its lowercased last segment and the
// qualifier before it. DuckDB resolves function names case-insensitively.
func funcRef(fc *pg.FuncCall, inFrom bool) parser.FuncRef {
	parts := make([]string, 0, len(fc.GetFuncname()))
	for _, n := range fc.GetFuncname() {
		parts = append(parts, strings.ToLower(n.GetString_().GetSval()))
	}
	ref := parser.FuncRef{InFrom: inFrom}
	if len(parts) == 0 {
		return ref
	}
	ref.Name = parts[len(parts)-1]
	ref.Qualifier = strings.Join(parts[:len(parts)-1], ".")
	// pg_query writes EXTRACT, SUBSTRING ... FROM, TRIM and similar syntax
	// as pg_catalog calls. The qualifier is not in the SQL text, so drop it.
	// A qualifier the caller wrote stays: DuckDB reads x.f(y) as f(x, y).
	if ref.Qualifier == "pg_catalog" && fc.GetFuncformat() == pg.CoercionForm_COERCE_SQL_SYNTAX {
		ref.Qualifier = ""
	}
	return ref
}

// sqlValueFunctionName maps a keyword such as CURRENT_USER or
// CURRENT_TIME(3) to a function name ("current_user", "current_time").
func sqlValueFunctionName(f *pg.SQLValueFunction) string {
	name := strings.ToLower(strings.TrimPrefix(f.GetOp().String(), "SVFOP_"))
	return strings.TrimSuffix(name, "_n")
}

// keywordCallName names the function behind a syntax node that is not a
// FuncCall, or "" for every other node.
func keywordCallName(n *pg.Node) string {
	switch v := n.Node.(type) {
	case *pg.Node_JsonArrayConstructor, *pg.Node_JsonArrayQueryConstructor:
		return "json_array"
	case *pg.Node_JsonObjectConstructor:
		return "json_object"
	case *pg.Node_JsonParseExpr:
		return "json"
	case *pg.Node_JsonScalarExpr:
		return "json_scalar"
	case *pg.Node_JsonSerializeExpr:
		return "json_serialize"
	case *pg.Node_JsonObjectAgg:
		return "json_objectagg"
	case *pg.Node_JsonArrayAgg:
		return "json_arrayagg"
	case *pg.Node_JsonFuncExpr:
		switch v.JsonFuncExpr.GetOp() {
		case pg.JsonExprOp_JSON_EXISTS_OP:
			return "json_exists"
		case pg.JsonExprOp_JSON_QUERY_OP:
			return "json_query"
		case pg.JsonExprOp_JSON_VALUE_OP:
			return "json_value"
		}
		return "json_table"
	case *pg.Node_XmlExpr:
		return strings.ToLower(strings.TrimPrefix(v.XmlExpr.GetOp().String(), "IS_"))
	case *pg.Node_XmlSerialize:
		return "xmlserialize"
	case *pg.Node_MergeSupportFunc:
		return "merge_action"
	}
	return ""
}

// isPathLike reports whether a table name part looks like a file path, a
// URL or a glob instead of an identifier.
func isPathLike(part string) bool {
	return strings.ContainsAny(part, `/\:*?[`)
}

// hasColumnOrSubquery reports whether m contains a column reference or a
// subquery anywhere below it.
func hasColumnOrSubquery(m protoreflect.Message) bool {
	switch m.Interface().(type) {
	case *pg.ColumnRef, *pg.SubLink, *pg.SelectStmt:
		return true
	}
	found := false
	eachChildMessage(m, func(child protoreflect.Message) bool {
		found = hasColumnOrSubquery(child)
		return !found
	})
	return found
}

// eachChildMessage calls fn for every populated message-typed field of m,
// including list elements. It stops when fn returns false.
func eachChildMessage(m protoreflect.Message, fn func(protoreflect.Message) bool) {
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				if !fn(list.Get(i).Message()) {
					return false
				}
			}
			return true
		}
		return fn(val.Message())
	})
}
