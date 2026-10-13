// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"cmp"
	"fmt"
	"reflect"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// wrapperSource is the alias of the table inside every wrapper. Every
// column sluice writes into a wrapper is qualified with it. DuckDB binds a
// qualified name to an outer FROM item or struct column of that name too,
// so the query may not use it.
const wrapperSource = "sluice_src"

// applyWrapTables replaces each RangeVar of a table with a row filter or a
// column mask with a wrapper, `(SELECT ... FROM t AS sluice_src WHERE
// <pred>) alias`. The alias the query wrote moves to the wrapper. A masked
// table's wrapper lists the cached columns with the SQL masks applied, so
// the rest of the statement only sees masked values. A table with only a
// filter keeps SELECT *. Every SelectStmt is walked: CTE bodies, FROM
// subqueries, set-operation arms and subqueries inside expressions.
func (s *state) applyWrapTables(raw *pg.ParseResult) error {
	if raw == nil || len(s.decision.RowFilters) == 0 && len(s.decision.ColumnMasks) == 0 {
		return nil
	}
	for _, stmt := range raw.Stmts {
		if stmt == nil {
			continue
		}
		if err := s.injectInNode(stmt.Stmt); err != nil {
			return err
		}
	}
	return s.checkWrapperQualifiers(raw)
}

// checkReservedName refuses the wrapper's inner alias anywhere in the
// statement, as a name or inside a literal that could become one, such as
// a JSON key.
func (s *state) checkReservedName(raw *pg.ParseResult) error {
	if len(s.decision.RowFilters) == 0 && len(s.decision.ColumnMasks) == 0 {
		return nil
	}
	return walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		found := false
		m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			if fd.Kind() == protoreflect.StringKind && !fd.IsList() {
				found = strings.Contains(foldName(v.String()), wrapperSource)
			}
			return !found
		})
		if found {
			return false, pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage(
				"the name " + wrapperSource + " is reserved, choose another name")
		}
		return true, nil
	})
}

// verifyWrapped refuses a statement that still reads a filtered or masked
// table directly. The wrap walk must reach every table; a form it misses
// fails here instead of being read raw.
func (s *state) verifyWrapped(raw *pg.ParseResult) error {
	if len(s.decision.RowFilters) == 0 && len(s.decision.ColumnMasks) == 0 {
		return nil
	}
	return walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		rv, ok := m.Interface().(*pg.RangeVar)
		if !ok {
			return true, nil
		}
		if _, inner := s.wrapped[rv]; inner {
			return false, nil
		}
		key := tableKeyOf(rv, s.defaultCatalog)
		if _, filtered := s.decision.RowFilters[key]; filtered || s.tableHasMask(key) {
			return false, fmt.Errorf("%w: table %s read without its policy wrapper", ErrUnsupportedSyntax, key)
		}
		return false, nil
	})
}

// checkWrapperQualifiers refuses a column qualified with the schema or
// catalog of a table sluice wrapped under its own name, such as
// main.t.id. The wrapper is named t alone, so DuckDB cannot resolve the
// column. Writing t.id instead could bind to another t in a nested SELECT.
func (s *state) checkWrapperQualifiers(raw *pg.ParseResult) error {
	var named []*pg.RangeVar
	for _, rv := range s.wrapped {
		if rv.Alias == nil || rv.Alias.Aliasname == "" {
			named = append(named, rv)
		}
	}
	if len(named) == 0 {
		return nil
	}
	return walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		cr, ok := m.Interface().(*pg.ColumnRef)
		if !ok {
			return true, nil
		}
		if len(cr.Fields) < 3 {
			return false, nil
		}
		f := make([]string, len(cr.Fields))
		for i, n := range cr.Fields {
			f[i] = foldName(n.GetString_().GetSval())
		}
		for _, rv := range named {
			if s.reachesTable(f, rv) {
				return false, pkgerr.New(pkgerr.CodeUnsupportedSyntax).WithMessage(fmt.Sprintf(
					"qualify the columns of table %q with the table name or an alias, not with its schema or catalog", foldName(rv.Relname)))
			}
		}
		return false, nil
	})
}

// reachesTable reports whether the folded fields f of a column reference
// name rv by its schema or catalog. DuckDB reads schema.t.col,
// catalog.t.col and catalog.schema.t.col. Other dotted forms, such as
// alias.struct_col.field, do not name rv. A table without a catalog is in
// DuckDB's default database. When its name is unknown, any catalog matches
// the four-part form, but the three-part form matches the schema only,
// since alias.struct_col.field has the same shape.
func (s *state) reachesTable(f []string, rv *pg.RangeVar) bool {
	name := foldName(rv.Relname)
	schema := cmp.Or(foldName(rv.Schemaname), "main")
	catalog := foldName(cmp.Or(rv.Catalogname, s.defaultCatalog, s.defaultDatabase))
	if len(f) >= 3 && f[1] == name && (f[0] == schema || catalog != "" && f[0] == catalog) {
		return true
	}
	return len(f) >= 4 && f[2] == name && f[1] == schema && (catalog == "" || f[0] == catalog)
}

func (s *state) injectInNode(n *pg.Node) error {
	if n == nil {
		return nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt:
		if v.SelectStmt != nil {
			if err := s.injectInSelect(v.SelectStmt); err != nil {
				return err
			}
		}
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil && v.RangeSubselect.Subquery != nil {
			if err := s.injectInNode(v.RangeSubselect.Subquery); err != nil {
				return err
			}
		}
	case *pg.Node_ExplainStmt:
		// Rewrite refuses EXPLAIN when a filter or mask applies. This arm
		// is a backstop in case that gate changes.
		if v.ExplainStmt != nil {
			if err := s.injectInNode(v.ExplainStmt.Query); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *state) injectInSelect(sel *pg.SelectStmt) error {
	if sel == nil {
		return nil
	}
	if sel.WithClause != nil {
		for _, cte := range sel.WithClause.Ctes {
			if cte == nil {
				continue
			}
			if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
				if err := s.injectInNode(c.CommonTableExpr.Ctequery); err != nil {
					return err
				}
			}
		}
	}
	for i, from := range sel.FromClause {
		replaced, err := s.injectInFromItem(from)
		if err != nil {
			return err
		}
		sel.FromClause[i] = replaced
	}
	if sel.Larg != nil {
		if err := s.injectInSelect(sel.Larg); err != nil {
			return err
		}
	}
	if sel.Rarg != nil {
		if err := s.injectInSelect(sel.Rarg); err != nil {
			return err
		}
	}
	// A subquery in an expression reads tables too. Without this a table
	// that is only named there would be read unfiltered.
	return eachExprSubquery(sel, s.injectInSelect)
}

// injectInFromItem maybe-wraps a FROM entry. Returns the (possibly
// rewritten) node to put back into the parent slice.
func (s *state) injectInFromItem(n *pg.Node) (*pg.Node, error) {
	if n == nil {
		return n, nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		return s.wrapRangeVar(v.RangeVar)
	case *pg.Node_JoinExpr:
		if v.JoinExpr == nil {
			return n, nil
		}
		larg, err := s.injectInFromItem(v.JoinExpr.Larg)
		if err != nil {
			return nil, err
		}
		rarg, err := s.injectInFromItem(v.JoinExpr.Rarg)
		if err != nil {
			return nil, err
		}
		v.JoinExpr.Larg = larg
		v.JoinExpr.Rarg = rarg
		if err := eachNodeSubquery(v.JoinExpr.Quals, s.injectInSelect); err != nil {
			return nil, err
		}
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil && v.RangeSubselect.Subquery != nil {
			if err := s.injectInNode(v.RangeSubselect.Subquery); err != nil {
				return nil, err
			}
		}
	default:
		// Any other FROM item (a function call, ...) can carry a subquery
		// in its arguments.
		if err := eachNodeSubquery(n, s.injectInSelect); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// wrapRangeVar returns the wrapper of rv when its table has a row filter
// or a column mask. Otherwise rv is returned as it is.
func (s *state) wrapRangeVar(rv *pg.RangeVar) (*pg.Node, error) {
	if rv == nil {
		return nil, nil
	}
	key := tableKeyOf(rv, s.defaultCatalog)
	filter, filtered := s.decision.RowFilters[key]
	masked := s.tableHasMask(key)
	if !filtered && !masked {
		return &pg.Node{Node: &pg.Node_RangeVar{RangeVar: rv}}, nil
	}

	// Preserve the existing alias; if none, synthesize one from the
	// relation name so outer references still resolve.
	alias := rv.Alias
	if alias == nil || alias.Aliasname == "" {
		// The deparse writes a lower-case name bare, and DuckDB reads some
		// names as a keyword: semi changes the join type and drops the
		// wrapper's name.
		if parser.IsUnsafeBareAlias(rv.Relname) {
			return nil, pkgerr.New(pkgerr.CodeUnsupportedSyntax).
				WithMessage(fmt.Sprintf("give table %q an alias", rv.Relname))
		}
		alias = &pg.Alias{Aliasname: rv.Relname}
	}

	src := &pg.RangeVar{
		Catalogname: rv.Catalogname,
		Schemaname:  rv.Schemaname,
		Relname:     rv.Relname,
		Inh:         rv.Inh,
		Location:    rv.Location,
		Alias:       &pg.Alias{Aliasname: wrapperSource},
	}
	inner := &pg.SelectStmt{
		TargetList: []*pg.Node{selectStarTarget()},
		FromClause: []*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: src}}},
	}
	// The filter params come first, then the mask params.
	if filtered {
		whereExpr, err := s.renderPredicate(filter.Predicate)
		if err != nil {
			return nil, fmt.Errorf("row filter %s: %w", key, err)
		}
		inner.WhereClause = whereExpr
		s.rewrites = append(s.rewrites, "row-filter-wrapped:"+key)
	}
	if masked {
		targets, err := s.projectColumns(key)
		if err != nil {
			return nil, err
		}
		inner.TargetList = targets
	}

	wrapped := &pg.RangeSubselect{
		Subquery: &pg.Node{Node: &pg.Node_SelectStmt{SelectStmt: inner}},
		Alias:    alias,
	}
	if s.wrapped == nil {
		s.wrapped = make(map[*pg.RangeVar]*pg.RangeVar)
	}
	s.wrapped[src] = rv
	return &pg.Node{Node: &pg.Node_RangeSubselect{RangeSubselect: wrapped}}, nil
}

// projectColumns lists the cached columns of a masked table in table
// order. A column the cache does not know is not read at all, so a column
// added at the source later cannot pass unmasked. A SQL mask is applied
// here and nowhere else. A post-query masked column passes through raw.
func (s *state) projectColumns(key string) ([]*pg.Node, error) {
	entry, err := s.tableEntry(key)
	if err != nil {
		return nil, err
	}
	if len(entry.Columns) == 0 {
		return nil, fmt.Errorf("%w: no columns known for %s", ErrSchemaMissing, key)
	}
	out := make([]*pg.Node, 0, len(entry.Columns))
	for _, col := range entry.Columns {
		ref := columnRefFromString(wrapperSource, col.Name)
		rt := &pg.ResTarget{Val: ref}
		if m, ok := s.maskForResolvedColumn(key, col.Name); ok && !s.isPostQueryMask(m) {
			expr, err := s.buildMaskExpr(m, key, col.Name, ref)
			if err != nil {
				return nil, fmt.Errorf("column %s.%s: %w", key, col.Name, err)
			}
			rt = &pg.ResTarget{Name: col.Name, Val: expr}
			s.rewrites = append(s.rewrites, "column-mask:"+key+"."+col.Name)
		}
		out = append(out, &pg.Node{Node: &pg.Node_ResTarget{ResTarget: rt}})
	}
	return out, nil
}

// selectStarTarget builds a ResTarget whose Val is a lone `*`.
func selectStarTarget() *pg.Node {
	cr := &pg.ColumnRef{Fields: []*pg.Node{{Node: &pg.Node_AStar{AStar: &pg.A_Star{}}}}}
	rt := &pg.ResTarget{Val: &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: cr}}}
	return &pg.Node{Node: &pg.Node_ResTarget{ResTarget: rt}}
}

// renderPredicate walks the compiled predicate tree and emits a WHERE
// expression AST. Templates are rendered against the current user and
// appended to s.params as positional `$N` placeholders.
func (s *state) renderPredicate(p *policy.CompiledPredicate) (*pg.Node, error) {
	if p == nil {
		return nil, nil
	}
	if len(p.All) > 0 {
		return s.renderBool(p.All, pg.BoolExprType_AND_EXPR)
	}
	if len(p.Any) > 0 {
		return s.renderBool(p.Any, pg.BoolExprType_OR_EXPR)
	}
	if p.Not != nil {
		inner, err := s.renderPredicate(p.Not)
		if err != nil {
			return nil, err
		}
		return &pg.Node{Node: &pg.Node_BoolExpr{BoolExpr: &pg.BoolExpr{
			Boolop: pg.BoolExprType_NOT_EXPR,
			Args:   []*pg.Node{inner},
		}}}, nil
	}
	return s.renderLeaf(p)
}

func (s *state) renderBool(children []*policy.CompiledPredicate, op pg.BoolExprType) (*pg.Node, error) {
	args := make([]*pg.Node, 0, len(children))
	for _, c := range children {
		n, err := s.renderPredicate(c)
		if err != nil {
			return nil, err
		}
		if n != nil {
			args = append(args, n)
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) == 1 {
		return args[0], nil
	}
	return &pg.Node{Node: &pg.Node_BoolExpr{BoolExpr: &pg.BoolExpr{Boolop: op, Args: args}}}, nil
}

// renderLeaf builds a Postgres AST fragment for one leaf predicate. All
// values are emitted as ParamRef placeholders; s.params retains the
// resolved Go values in positional order.
func (s *state) renderLeaf(p *policy.CompiledPredicate) (*pg.Node, error) {
	col := columnRefFromString(wrapperSource, p.Column)
	switch string(p.Op) {
	case "IsNull", "IsNotNull":
		return s.renderNullCheck(col, string(p.Op)), nil
	}
	op := string(p.Op)
	values, err := s.renderValues(p.Values, op == "In" || op == "NotIn")
	if err != nil {
		return nil, err
	}
	return s.renderBinary(col, op, values)
}

func (s *state) renderNullCheck(col *pg.Node, op string) *pg.Node {
	kind := pg.NullTestType_IS_NULL
	if op == "IsNotNull" {
		kind = pg.NullTestType_IS_NOT_NULL
	}
	return &pg.Node{Node: &pg.Node_NullTest{NullTest: &pg.NullTest{Arg: col, Nulltesttype: kind}}}
}

// renderValues binds each value as a parameter. A template can give a
// list, such as {{ subject.groups }}. With list set (In, NotIn) each
// element becomes one parameter. Any other value that is not a single
// scalar cannot be compared, and the subject is treated as lacking it.
func (s *state) renderValues(vs []policy.ValueSource, list bool) ([]*pg.Node, error) {
	out := make([]*pg.Node, 0, len(vs))
	bind := func(val any) {
		s.params = append(s.params, val)
		out = append(out, paramRef(len(s.params)))
	}
	for _, v := range vs {
		if v.Template == nil {
			bind(v.Literal)
			continue
		}
		val, err := v.Template.Render(s.user, s.facts)
		if err != nil {
			return nil, err
		}
		rv := reflect.ValueOf(val)
		switch {
		case !rv.IsValid() || isScalar(rv):
			bind(val)
		case rv.Kind() == reflect.Slice && list:
			for i := 0; i < rv.Len(); i++ {
				el := reflect.ValueOf(rv.Index(i).Interface())
				if el.IsValid() && !isScalar(el) {
					return nil, fmt.Errorf("%w: %s holds a value that is not a scalar", policy.ErrTemplateVarMissing, v.Template.Raw)
				}
				bind(rv.Index(i).Interface())
			}
		default:
			return nil, fmt.Errorf("%w: %s is not a single value", policy.ErrTemplateVarMissing, v.Template.Raw)
		}
	}
	return out, nil
}

func isScalar(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct, reflect.Pointer, reflect.Interface:
		return false
	}
	return true
}

func (s *state) renderBinary(col *pg.Node, op string, values []*pg.Node) (*pg.Node, error) {
	switch op {
	case "Equals":
		return opExpr(col, "=", values[0]), nil
	case "NotEquals":
		return opExpr(col, "<>", values[0]), nil
	case "GreaterThan":
		return opExpr(col, ">", values[0]), nil
	case "GreaterThanOrEqual":
		return opExpr(col, ">=", values[0]), nil
	case "LessThan":
		return opExpr(col, "<", values[0]), nil
	case "LessThanOrEqual":
		return opExpr(col, "<=", values[0]), nil
	case "Like":
		return opExpr(col, "~~", values[0]), nil
	case "NotLike":
		return opExpr(col, "!~~", values[0]), nil
	case "In":
		return inExpr(col, values, false), nil
	case "NotIn":
		return inExpr(col, values, true), nil
	case "Between":
		return betweenExpr(col, values[0], values[1], false), nil
	case "StartsWith":
		return funcExpr("starts_with", col, values[0]), nil
	case "EndsWith":
		return funcExpr("ends_with", col, values[0]), nil
	case "Contains":
		return funcExpr("contains", col, values[0]), nil
	case "Matches":
		// Partial-match semantics (DuckDB regexp_matches); anchor with ^…$
		// for a full match. DuckDB's ~ operator is a full match and must
		// not be used here.
		return funcExpr("regexp_matches", col, values[0]), nil
	}
	return nil, fmt.Errorf("%w: unsupported predicate op %q", ErrUnsupportedSyntax, op)
}

// funcExpr renders a plain function call. String operators render as
// DuckDB functions rather than LIKE patterns so parameter values stay
// literal — no pattern-metacharacter escaping anywhere.
func funcExpr(name string, args ...*pg.Node) *pg.Node {
	return pg.MakeFuncCallNode([]*pg.Node{pg.MakeStrNode(name)}, args, -1)
}

func opExpr(l *pg.Node, op string, r *pg.Node) *pg.Node {
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  pg.A_Expr_Kind_AEXPR_OP,
		Name:  []*pg.Node{pg.MakeStrNode(op)},
		Lexpr: l,
		Rexpr: r,
	}}}
}

func inExpr(col *pg.Node, values []*pg.Node, notIn bool) *pg.Node {
	// A list template can give no value: nothing is IN an empty list, and
	// everything is NOT IN it.
	if len(values) == 0 {
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
			Val: &pg.A_Const_Boolval{Boolval: &pg.Boolean{Boolval: notIn}},
		}}}
	}
	// IN / NOT IN deparses from a List rexpr.
	listNode := &pg.Node{Node: &pg.Node_List{List: &pg.List{Items: values}}}
	name := "="
	kind := pg.A_Expr_Kind_AEXPR_IN
	if notIn {
		name = "<>"
	}
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  kind,
		Name:  []*pg.Node{pg.MakeStrNode(name)},
		Lexpr: col,
		Rexpr: listNode,
	}}}
}

func betweenExpr(col, lo, hi *pg.Node, notBetween bool) *pg.Node {
	kind := pg.A_Expr_Kind_AEXPR_BETWEEN
	if notBetween {
		kind = pg.A_Expr_Kind_AEXPR_NOT_BETWEEN
	}
	list := &pg.Node{Node: &pg.Node_List{List: &pg.List{Items: []*pg.Node{lo, hi}}}}
	return &pg.Node{Node: &pg.Node_AExpr{AExpr: &pg.A_Expr{
		Kind:  kind,
		Name:  []*pg.Node{pg.MakeStrNode("BETWEEN")},
		Lexpr: col,
		Rexpr: list,
	}}}
}

// columnRefFromString returns the ColumnRef qualifier.name. name is one
// identifier, also when it holds a dot.
func columnRefFromString(qualifier, name string) *pg.Node {
	return &pg.Node{Node: &pg.Node_ColumnRef{ColumnRef: &pg.ColumnRef{
		Fields: []*pg.Node{pg.MakeStrNode(qualifier), pg.MakeStrNode(name)},
	}}}
}

func paramRef(n int) *pg.Node {
	return &pg.Node{Node: &pg.Node_ParamRef{ParamRef: &pg.ParamRef{Number: int32(n)}}}
}
