// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"fmt"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/schema"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// applySubstituteMask walks every target-list ColumnRef, WHERE/HAVING
// expression, and JOIN qualifier in the cloned AST and replaces each
// masked column reference with the mask provider's SQL expression.
//
// MVP mask set: null + constant (pkg/mask builtins). The substitution is
// a pure AST mutation — no text-level interpolation. Aliases are
// preserved so downstream consumers see the expected column names.
func (s *state) applySubstituteMask(raw *pg.ParseResult) error {
	if raw == nil || len(s.decision.ColumnMasks) == 0 {
		return nil
	}
	// Post-query masks (FPE, fake, jitter, hmac) are planned first: bare
	// top-level select items are recorded for Go-side masking, and any
	// other use of such a column is refused. The SQL substitution pass
	// then leaves those columns untouched.
	if s.hasPostQueryMasks() {
		if err := s.planPostMasks(raw); err != nil {
			return err
		}
	}
	for _, stmt := range raw.Stmts {
		if stmt == nil {
			continue
		}
		if err := s.maskInNode(stmt.Stmt, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) maskInNode(n *pg.Node, aliases aliasMap) error {
	if n == nil {
		return nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt:
		if v.SelectStmt != nil {
			return s.maskInSelect(v.SelectStmt, aliases)
		}
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect != nil {
			return s.maskInNode(v.RangeSubselect.Subquery, aliases)
		}
	case *pg.Node_ExplainStmt:
		// Policies match tables inside EXPLAIN bodies (the table walker
		// descends into them), so the rewrite must too.
		if v.ExplainStmt != nil {
			return s.maskInNode(v.ExplainStmt.Query, aliases)
		}
	}
	return nil
}

func (s *state) maskInSelect(sel *pg.SelectStmt, outerAliases aliasMap) error {
	if sel == nil {
		return nil
	}

	// Build the alias map for this SELECT so column resolution sees the
	// wrapped row-filter subqueries as their original tables.
	aliases := aliasMap{}
	for alias, ref := range outerAliases {
		aliases[alias] = ref
	}
	local := aliasMap{}
	for _, f := range sel.FromClause {
		s.recordFromItemForMask(f, local)
	}
	for alias, ref := range local {
		// DuckDB falls back to the outer table when the inner one lacks the
		// column, the lookup below does not.
		if outer, ok := outerAliases[alias]; ok {
			outerKey := outer.Catalog + "." + outer.Schema + "." + outer.Table
			if outerKey != ref.Catalog+"."+ref.Schema+"."+ref.Table && s.tableHasMask(outerKey) {
				return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"the name %q hides an outer table with masked columns, give it another alias", alias))
			}
		}
		aliases[alias] = ref
	}
	s.selectDepth++
	prevLocal := s.localAliases
	s.localAliases = local
	defer func() {
		s.selectDepth--
		s.localAliases = prevLocal
	}()

	// Recurse into CTE bodies and subqueries first. A CTE body cannot see
	// the FROM items of the SELECT that defines it.
	if sel.WithClause != nil {
		for _, cte := range sel.WithClause.Ctes {
			if cte == nil {
				continue
			}
			if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
				if err := s.maskInNode(c.CommonTableExpr.Ctequery, outerAliases); err != nil {
					return err
				}
			}
		}
	}
	for _, f := range sel.FromClause {
		// A row-filter wrapper holds `SELECT *` and the policy predicate,
		// nothing the caller wrote. Masking it would mask the predicate.
		if rs, ok := f.Node.(*pg.Node_RangeSubselect); ok && rs.RangeSubselect != nil && !s.isWrapper(rs.RangeSubselect) {
			if err := s.maskInNode(rs.RangeSubselect.Subquery, aliases); err != nil {
				return err
			}
		}
		if j, ok := f.Node.(*pg.Node_JoinExpr); ok && j.JoinExpr != nil {
			if err := s.maskInJoin(j.JoinExpr, aliases); err != nil {
				return err
			}
		}
	}
	if sel.Larg != nil {
		if err := s.maskInSelect(sel.Larg, aliases); err != nil {
			return err
		}
	}
	if sel.Rarg != nil {
		if err := s.maskInSelect(sel.Rarg, aliases); err != nil {
			return err
		}
	}

	// Output names the caller wrote, before substitution adds its own.
	outputNames := map[string]bool{}
	for _, t := range sel.TargetList {
		if name := t.GetResTarget().GetName(); name != "" {
			outputNames[foldName(name)] = true
		}
	}

	// TargetList. A bare column ref is substituted in place (preserving
	// the column alias); anything else — function calls, CASE, casts,
	// arithmetic — is walked so refs nested inside it are substituted too.
	for i, t := range sel.TargetList {
		rt, ok := t.Node.(*pg.Node_ResTarget)
		if !ok || rt.ResTarget == nil {
			continue
		}
		newVal, origName, err := s.maybeSubstituteColumnRef(rt.ResTarget.Val, aliases)
		if err != nil {
			return err
		}
		if newVal == nil {
			if err := s.maskInExpr(rt.ResTarget.Val, aliases); err != nil {
				return err
			}
			continue
		}
		// Preserve column alias so the client sees the expected name.
		if rt.ResTarget.Name == "" && origName != "" {
			rt.ResTarget.Name = origName
		}
		rt.ResTarget.Val = newVal
		sel.TargetList[i] = t
	}

	// Every other expression-bearing clause.
	if err := s.maskExprSlot(&sel.WhereClause, aliases); err != nil {
		return err
	}
	if err := s.maskExprSlot(&sel.HavingClause, aliases); err != nil {
		return err
	}
	for _, list := range [][]*pg.Node{
		sel.GroupClause, sel.DistinctClause, sel.WindowClause,
	} {
		if err := s.maskExprList(list, aliases); err != nil {
			return err
		}
	}
	for i := range sel.SortClause {
		// `count(*) AS customers ... ORDER BY customers` sorts by the
		// output column, not by the table of that name.
		if name := loneColumnName(sel.SortClause[i].GetSortBy().GetNode()); name != "" && outputNames[name] {
			// An ambiguous name is fine here too: the output column it
			// sorts by is already masked.
			if _, _, masked, err := s.lookupMaskForColumn(aliases, "", name); err != nil || !masked {
				continue
			}
		}
		if err := s.maskExprSlot(&sel.SortClause[i], aliases); err != nil {
			return err
		}
	}
	if err := s.maskExprSlot(&sel.LimitCount, aliases); err != nil {
		return err
	}
	if err := s.maskExprSlot(&sel.LimitOffset, aliases); err != nil {
		return err
	}
	// VALUES rows. `(VALUES (c.email))` in a subquery reads the outer column.
	return s.maskExprList(sel.ValuesLists, aliases)
}

// maskExprSlot offers the node itself for substitution (a bare masked
// column ref) and otherwise walks its expression tree.
func (s *state) maskExprSlot(slot **pg.Node, aliases aliasMap) error {
	if slot == nil || *slot == nil {
		return nil
	}
	repl, _, err := s.maybeSubstituteColumnRef(*slot, aliases)
	if err != nil {
		return err
	}
	if repl != nil {
		*slot = repl
		return nil
	}
	return s.maskInExpr(*slot, aliases)
}

func (s *state) maskExprList(list []*pg.Node, aliases aliasMap) error {
	for i := range list {
		if err := s.maskExprSlot(&list[i], aliases); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) maskInJoin(j *pg.JoinExpr, aliases aliasMap) error {
	if j == nil {
		return nil
	}
	for _, arg := range []*pg.Node{j.Larg, j.Rarg} {
		switch v := arg.GetNode().(type) {
		case *pg.Node_RangeSubselect:
			if v.RangeSubselect != nil && !s.isWrapper(v.RangeSubselect) {
				if err := s.maskInNode(v.RangeSubselect.Subquery, aliases); err != nil {
					return err
				}
			}
		case *pg.Node_JoinExpr:
			if err := s.maskInJoin(v.JoinExpr, aliases); err != nil {
				return err
			}
		}
	}
	return s.maskExprSlot(&j.Quals, aliases)
}

func (s *state) isWrapper(rs *pg.RangeSubselect) bool {
	_, ok := s.wrappers[rs]
	return ok
}

// maskInExpr performs in-place substitution anywhere inside an
// expression tree. Nested query scopes re-enter the structured walk so
// alias maps rebuild; every other node is descended generically via
// protobuf reflection, so no expression context (function args, CASE
// arms, casts, sort keys, …) can carry a masked column unsubstituted.
func (s *state) maskInExpr(n *pg.Node, aliases aliasMap) error {
	if n == nil {
		return nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_SelectStmt, *pg.Node_RangeSubselect, *pg.Node_ExplainStmt:
		return s.maskInNode(n, aliases)
	case *pg.Node_SubLink:
		if v.SubLink == nil {
			return nil
		}
		if err := s.maskInNode(v.SubLink.Subselect, aliases); err != nil {
			return err
		}
		// The test expression (left-hand side of IN/ANY) belongs to the
		// enclosing scope, not the subselect.
		return s.maskExprSlot(&v.SubLink.Testexpr, aliases)
	}
	return s.maskWalkMessage(n.ProtoReflect(), aliases)
}

// maskWalkMessage descends into every populated message field looking
// for *pg.Node slots. A node that is a masked bare column ref is
// replaced in place; anything else recurses through maskInExpr so
// nested scopes keep their delegation.
func (s *state) maskWalkMessage(m protoreflect.Message, aliases aliasMap) error {
	var walkErr error
	m.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			list := val.List()
			for i := 0; i < list.Len(); i++ {
				item := list.Get(i).Message()
				node, ok := item.Interface().(*pg.Node)
				if !ok {
					if walkErr = s.maskWalkMessage(item, aliases); walkErr != nil {
						return false
					}
					continue
				}
				repl, _, err := s.maybeSubstituteColumnRef(node, aliases)
				if err != nil {
					walkErr = err
					return false
				}
				if repl != nil {
					list.Set(i, protoreflect.ValueOfMessage(repl.ProtoReflect()))
					continue
				}
				if walkErr = s.maskInExpr(node, aliases); walkErr != nil {
					return false
				}
			}
			return true
		}
		msg := val.Message()
		node, ok := msg.Interface().(*pg.Node)
		if !ok {
			walkErr = s.maskWalkMessage(msg, aliases)
			return walkErr == nil
		}
		repl, _, err := s.maybeSubstituteColumnRef(node, aliases)
		if err != nil {
			walkErr = err
			return false
		}
		if repl != nil {
			m.Set(fd, protoreflect.ValueOfMessage(repl.ProtoReflect()))
			return true
		}
		walkErr = s.maskInExpr(node, aliases)
		return walkErr == nil
	})
	return walkErr
}

// maybeSubstituteColumnRef inspects n; if it is a ColumnRef that
// resolves (via aliases) to a masked column, returns the replacement
// expression node. Otherwise returns (nil, "", nil).
func (s *state) maybeSubstituteColumnRef(n *pg.Node, aliases aliasMap) (*pg.Node, string, error) {
	if n == nil {
		return nil, "", nil
	}
	cr, ok := n.Node.(*pg.Node_ColumnRef)
	if !ok || cr.ColumnRef == nil {
		return nil, "", nil
	}
	qualifier, column := splitColumnRef(cr.ColumnRef)
	if column == "" {
		return nil, "", nil
	}
	mask, tableKey, ok, err := s.lookupMaskForColumn(aliases, qualifier, column)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		if err := s.checkWholeRowRef(cr.ColumnRef, aliases); err != nil {
			return nil, "", err
		}
		return nil, "", s.checkFieldAccess(cr.ColumnRef, aliases)
	}
	// Post-query masks are handled by planPostMasks; leave the column ref
	// intact here so the value returns raw for Go-side masking. That only
	// works for the top-level select list. A nested SELECT would hand the
	// raw value to its parent under a name planPostMasks cannot trace.
	if s.isPostQueryMask(mask) {
		if s.selectDepth > 1 {
			return nil, "", ErrMaskPostQueryContext
		}
		return nil, "", nil
	}
	expr, err := s.buildMaskExpr(mask, tableKey, column, n)
	if err != nil {
		return nil, "", fmt.Errorf("column %s.%s: %w", tableKey, column, err)
	}
	s.rewrites = append(s.rewrites, "column-mask:"+tableKey+"."+column)
	return expr, column, nil
}

// loneColumnName returns the lower-case name of an unqualified column
// reference, or "" for anything else.
func loneColumnName(n *pg.Node) string {
	cr := n.GetColumnRef()
	if cr == nil || len(cr.Fields) != 1 {
		return ""
	}
	return foldName(cr.Fields[0].GetString_().GetSval())
}

// checkWholeRowRef refuses a name that is a table and not a column: the
// alias alone (`c`) or the table behind its schema or catalog
// (`main.customers`). DuckDB returns the whole row as a struct for it.
// Masks are substituted where a column is referenced, so the struct would
// carry the masked columns raw, also into to_json(c), (c).email or
// c['email'].
func (s *state) checkWholeRowRef(cr *pg.ColumnRef, aliases aliasMap) error {
	n := len(cr.Fields)
	if n == 0 || n > 3 {
		return nil
	}
	name := foldName(cr.Fields[n-1].GetString_().GetSval())
	ref, ok := aliases[name]
	if !ok || !s.tableHasMask(ref.Catalog+"."+ref.Schema+"."+ref.Table) {
		return nil
	}
	if n == 1 {
		// A real column with that name wins over the table in DuckDB, but
		// only a column of this SELECT's own FROM items. A column of an
		// outer table does not: the inner alias is found first.
		if s.columnInScope(name, s.localAliases) {
			return nil
		}
	} else {
		// `t.col` is a column when t is a table in scope that has it.
		q := foldName(cr.Fields[n-2].GetString_().GetSval())
		if qref, ok := aliases[q]; ok && s.columnInScope(name, aliasMap{q: qref}) {
			return nil
		}
	}
	return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
		"%q names a table with masked columns and cannot be used as a value, select its columns by name", name))
}

// checkFieldAccess refuses dotted access into a masked column
// (`info.ssn`, `p.info.ssn` for a STRUCT column info). pg_query reads the
// last name as the column, so the normal lookup finds no mask.
func (s *state) checkFieldAccess(cr *pg.ColumnRef, aliases aliasMap) error {
	for i := 0; i < len(cr.Fields)-1; i++ {
		column := cr.Fields[i].GetString_().GetSval()
		if column == "" {
			continue
		}
		qualifier := ""
		scope := aliases
		if i > 0 {
			qualifier = foldName(cr.Fields[i-1].GetString_().GetSval())
			ref, ok := aliases[qualifier]
			if !ok {
				continue
			}
			scope = aliasMap{qualifier: ref}
		}
		m, _, masked, err := s.lookupMaskForColumn(scope, qualifier, column)
		if err == nil && !masked {
			continue
		}
		// A wildcard mask matches any name, also a table alias. Only a
		// real column counts. Without a schema cache trust literal masks.
		if s.schema != nil {
			if !s.columnInScope(column, scope) {
				continue
			}
		} else if m == nil || strings.Contains(m.Column, "*") {
			continue
		}
		return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
			"field access on masked column %q is not supported", column))
	}
	return nil
}

func (s *state) tableHasMask(tableKey string) bool {
	for _, m := range s.decision.ColumnMasks {
		if m.TableKey == tableKey {
			return true
		}
	}
	return false
}

// columnInScope reports whether a table in scope has a column called name.
// Without a schema cache this cannot be shown, so the answer is no.
func (s *state) columnInScope(name string, aliases aliasMap) bool {
	if s.schema == nil {
		return false
	}
	for _, ref := range aliases {
		entry, err := s.schemaEntry(schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table})
		if err != nil {
			continue
		}
		for _, col := range entry.Columns {
			if strings.EqualFold(col.Name, name) {
				return true
			}
		}
	}
	return false
}

// checkMaskedAliases refuses FROM aliases that give the columns of a masked
// table another name: a column alias list (`t AS x(a, b)`) and an alias on
// a join (`(a JOIN b ON ...) AS j`). The mask pass resolves a column by its
// own name under the table's alias and cannot follow either form. It must
// run before row filters wrap the tables.
func (s *state) checkMaskedAliases(raw *pg.ParseResult) error {
	if raw == nil || len(s.decision.ColumnMasks) == 0 {
		return nil
	}
	refuse := func(what string) error {
		return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
			what + " on a table with masked columns is not supported, refer to the columns by their own names")
	}
	return walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.RangeVar:
			if len(v.GetAlias().GetColnames()) > 0 && s.rangeVarHasMask(v) {
				return false, refuse("a column alias list")
			}
		case *pg.JoinExpr:
			if (v.Alias != nil || v.JoinUsingAlias != nil) && s.joinHasMaskedTable(v) {
				return false, refuse("a join alias")
			}
		case *pg.SelectStmt:
			// Two FROM items under one name: the mask pass can only keep
			// one of them, and the other table's masks would be skipped.
			if dup := duplicateFromName(v.FromClause, map[string]bool{}); dup != "" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"the name %q is used for more than one FROM item, give each its own alias", dup))
			}
		case *pg.ResTarget:
			// A star that is a select item itself is expanded before masking.
			if isStarRef(v.GetVal().GetColumnRef()) {
				return false, nil
			}
		case *pg.ColumnRef:
			// DuckDB reads `c.* LIKE 'email'` as the columns of c that match.
			if isStarRef(v) {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"a star inside an expression is not supported with column masks, list the columns")
			}
		case *pg.A_Expr:
			// DuckDB reads `#3` as the third column of the FROM list.
			if v.Lexpr == nil && len(v.Name) == 1 && v.Name[0].GetString_().GetSval() == "#" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"positional column references (#N) are not supported with column masks")
			}
		}
		return true, nil
	})
}

func isStarRef(cr *pg.ColumnRef) bool {
	n := len(cr.GetFields())
	return n > 0 && cr.Fields[n-1].GetAStar() != nil
}

// duplicateFromName returns a name that two items of one FROM list share.
func duplicateFromName(items []*pg.Node, seen map[string]bool) string {
	for _, n := range items {
		name := ""
		switch v := n.GetNode().(type) {
		case *pg.Node_RangeVar:
			name = aliasKey(v.RangeVar)
		case *pg.Node_RangeSubselect:
			name = foldName(v.RangeSubselect.GetAlias().GetAliasname())
		case *pg.Node_RangeFunction:
			name = foldName(v.RangeFunction.GetAlias().GetAliasname())
		case *pg.Node_JoinExpr:
			if dup := duplicateFromName([]*pg.Node{v.JoinExpr.Larg, v.JoinExpr.Rarg}, seen); dup != "" {
				return dup
			}
		}
		if name == "" {
			continue
		}
		if seen[name] {
			return name
		}
		seen[name] = true
	}
	return ""
}

func (s *state) rangeVarHasMask(rv *pg.RangeVar) bool {
	return s.tableHasMask(tableKeyOf(rv, s.defaultCatalog))
}

// joinHasMaskedTable looks at the tables joined directly. A subquery in
// the join is masked in its own SELECT and does not count.
func (s *state) joinHasMaskedTable(j *pg.JoinExpr) bool {
	for _, arg := range []*pg.Node{j.GetLarg(), j.GetRarg()} {
		if rv := arg.GetRangeVar(); rv != nil && s.rangeVarHasMask(rv) {
			return true
		}
		if inner := arg.GetJoinExpr(); inner != nil && s.joinHasMaskedTable(inner) {
			return true
		}
	}
	return false
}

// lacksColumn reports whether the schema cache shows that the table has no
// column of that name. Unknown counts as "may have it".
func (s *state) lacksColumn(ref parser.TableRef, column string) bool {
	if s.schema == nil {
		return false
	}
	entry, err := s.schemaEntry(schema.Key{Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table})
	if err != nil {
		return false
	}
	for _, col := range entry.Columns {
		if strings.EqualFold(col.Name, column) {
			return false
		}
	}
	return true
}

// schemaEntry looks a table up in the schema cache without regard to
// case. Keys in this package are lower case, the cache keeps the names the
// source reports.
func (s *state) schemaEntry(key schema.Key) (*schema.Entry, error) {
	folded := strings.ToLower(key.Catalog + "." + key.Schema + "." + key.Table)
	if s.schemaIndex == nil {
		s.indexSchema()
	}
	if e, ok := s.schemaIndex[folded]; ok {
		return e, nil
	}
	// Not cached under any case: Get loads the catalog.
	entry, err := s.schema.Get(s.ctx, key)
	if err == nil {
		return entry, nil
	}
	s.indexSchema()
	if e, ok := s.schemaIndex[folded]; ok {
		return e, nil
	}
	return nil, err
}

func (s *state) indexSchema() {
	s.schemaIndex = make(map[string]*schema.Entry)
	for _, e := range s.schema.All() {
		k := strings.ToLower(e.Key.Catalog + "." + e.Key.Schema + "." + e.Key.Table)
		// Two tables that differ only in case: keep the lower-case one.
		if prev, dup := s.schemaIndex[k]; dup && prev.Key.Table == strings.ToLower(prev.Key.Table) {
			continue
		}
		s.schemaIndex[k] = e
	}
}

// lookupMaskForColumn returns the mask registered for the column under
// the resolved TableRef. A qualifier matches alias-exact; if no
// qualifier was given we walk every aliased table looking for a unique
// hit. Mask column selectors may be wildcard patterns ("*", "ssn_*"), so
// resolution matches the actual column against each mask's pattern rather
// than doing an exact key lookup.
func (s *state) lookupMaskForColumn(aliases aliasMap, qualifier, column string) (*policy.CompiledMask, string, bool, error) {
	if qualifier != "" {
		ref, ok := aliases[foldName(qualifier)]
		if !ok {
			return nil, "", false, nil
		}
		tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
		if m, ok := s.maskForResolvedColumn(tk, column); ok {
			return m, tk, true, nil
		}
		return nil, "", false, nil
	}
	// DuckDB binds an unqualified name to the innermost SELECT that has
	// it, so the tables of the current SELECT are asked first.
	for _, scope := range []aliasMap{s.localAliases, aliases} {
		var hit *policy.CompiledMask
		var hitKey string
		for alias, ref := range scope {
			if _, visible := aliases[alias]; !visible {
				continue
			}
			tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
			m, ok := s.maskForResolvedColumn(tk, column)
			if !ok {
				continue
			}
			// A wildcard mask matches any name. A table that is known not
			// to have the column cannot be the one the name refers to.
			if s.lacksColumn(ref, column) {
				continue
			}
			if hit != nil && hitKey != tk {
				// Two tables of one scope mask a column of this name.
				// Serving the reference could leave it unmasked.
				return nil, "", false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"column %q is masked in more than one table in scope, qualify it with the table name", column))
			}
			hit = m
			hitKey = tk
		}
		if hit != nil {
			return hit, hitKey, true, nil
		}
	}
	return nil, "", false, nil
}

// maskForResolvedColumn returns the highest-precedence mask on tableKey
// whose column pattern matches column. Precedence: priority desc, then
// column specificity desc (literal beats wildcard), then policy name asc —
// matching the engine's mask conflict order.
func (s *state) maskForResolvedColumn(tableKey, column string) (*policy.CompiledMask, bool) {
	var best *policy.CompiledMask
	for _, m := range s.decision.ColumnMasks {
		if m.TableKey != tableKey || !maskColumnMatches(m.Column, column) {
			continue
		}
		if best == nil || maskMoreSpecific(m, best) {
			best = m
		}
	}
	return best, best != nil
}

// maskColumnMatches reports whether a mask column selector pattern matches
// the concrete column name. Literal patterns compare exactly (fast path);
// wildcard patterns compile through the apitypes wildcard grammar.
func maskColumnMatches(pattern, column string) bool {
	// DuckDB resolves column names without regard to case, also when quoted.
	pattern, column = strings.ToLower(pattern), strings.ToLower(column)
	if pattern == column {
		return true
	}
	if !strings.ContainsAny(pattern, "*") {
		return false
	}
	m, err := apitypes.CompileWildcard(pattern)
	if err != nil {
		return false
	}
	return m.Match(column)
}

// maskMoreSpecific reports whether mask a outranks mask b under the mask
// conflict order (priority desc, specificity desc, name asc).
func maskMoreSpecific(a, b *policy.CompiledMask) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	as, bs := colSpecificity(a.Column), colSpecificity(b.Column)
	if as != bs {
		return as > bs
	}
	return a.Policy < b.Policy
}

// colSpecificity scores a column pattern: literal names (no wildcard) rank
// above any wildcard pattern; among wildcards, more literal characters wins.
func colSpecificity(pattern string) int {
	if !strings.ContainsAny(pattern, "*") {
		return 1000 + len(pattern)
	}
	return len(strings.ReplaceAll(pattern, "*", ""))
}

// splitColumnRef returns (qualifier, columnName) from a ColumnRef's
// Fields. Qualifier is empty for an unqualified reference; columnName is
// empty if the ref is `*` or unparseable.
func splitColumnRef(cr *pg.ColumnRef) (string, string) {
	if cr == nil || len(cr.Fields) == 0 {
		return "", ""
	}
	last := cr.Fields[len(cr.Fields)-1]
	sNode, ok := last.Node.(*pg.Node_String_)
	if !ok || sNode.String_ == nil {
		return "", ""
	}
	column := sNode.String_.Sval
	qualifier := ""
	if len(cr.Fields) > 1 {
		qNode, ok := cr.Fields[len(cr.Fields)-2].Node.(*pg.Node_String_)
		if ok && qNode.String_ != nil {
			qualifier = qNode.String_.Sval
		}
	}
	return qualifier, column
}

// buildMaskExpr returns the AST node that replaces the masked ColumnRef.
// null and constant keep their literal fast paths (byte-identical output
// to the MVP); every other type goes through the provider's MaskSQL
// snippet, parsed and spliced around a clone of the original reference.
func (s *state) buildMaskExpr(m *policy.CompiledMask, tableKey, column string, orig *pg.Node) (*pg.Node, error) {
	switch m.Type {
	case apitypes.MaskNull:
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{Isnull: true}}}, nil
	case apitypes.MaskConstant:
		return constExpr(m.Args.Value)
	}
	reg := s.masks
	if reg == nil {
		reg = pkgmask.Default()
	}
	provider, ok := reg.Lookup(string(m.Type))
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrMaskUnsupported, m.Type)
	}
	catalog, schemaName, table := splitTableKey(tableKey)
	snippet, params, err := provider.MaskSQL(pkgmask.MaskContext{
		Ctx:       s.ctx,
		Column:    pkgmask.ColumnRef{Catalog: catalog, Schema: schemaName, Table: table, Column: column},
		Args:      m.Args,
		Identity:  maskIdentity{s.user},
		SaltStore: s.salts,
	})
	if err != nil {
		return nil, fmt.Errorf("mask %s: %w", m.Type, err)
	}
	return s.renderMaskSnippet(snippet, params, orig)
}

// splitTableKey splits "catalog.schema.table" into its parts. Missing
// segments stay empty — the key format is produced by the policy engine.
func splitTableKey(key string) (catalog, schemaName, table string) {
	parts := strings.SplitN(key, ".", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return "", parts[0], parts[1]
	default:
		return "", "", key
	}
}

// constExpr turns a Go value into an A_Const protobuf node. Supported
// types: string, bool, int / int32 / int64, float64. Other types fall
// through to a string representation, which is safe for SQL literals
// (identifiers never flow through here).
func constExpr(v any) (*pg.Node, error) {
	switch x := v.(type) {
	case nil:
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{Isnull: true}}}, nil
	case string:
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
			Val: &pg.A_Const_Sval{Sval: &pg.String{Sval: x}},
		}}}, nil
	case bool:
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
			Val: &pg.A_Const_Boolval{Boolval: &pg.Boolean{Boolval: x}},
		}}}, nil
	case int:
		return intConst(int64(x)), nil
	case int32:
		return intConst(int64(x)), nil
	case int64:
		return intConst(x), nil
	case float64:
		return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
			Val: &pg.A_Const_Fval{Fval: &pg.Float{Fval: fmt.Sprintf("%v", x)}},
		}}}, nil
	}
	return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
		Val: &pg.A_Const_Sval{Sval: &pg.String{Sval: fmt.Sprintf("%v", v)}},
	}}}, nil
}

func intConst(n int64) *pg.Node {
	return &pg.Node{Node: &pg.Node_AConst{AConst: &pg.A_Const{
		Val: &pg.A_Const_Ival{Ival: &pg.Integer{Ival: int32(n)}},
	}}}
}

// recordFromItemForMask registers the tables of one FROM item under the
// names a query can use to qualify their columns. A row-filter wrapper
// stands for its table. A subquery the caller wrote is not registered: its
// columns are masked in its own SELECT, and a column it derives under the
// name of a masked column is not that column.
func (s *state) recordFromItemForMask(n *pg.Node, out aliasMap) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		if v.RangeVar != nil {
			recordTable(v.RangeVar, out, s.defaultCatalog)
		}
	case *pg.Node_JoinExpr:
		if v.JoinExpr != nil {
			s.recordFromItemForMask(v.JoinExpr.Larg, out)
			s.recordFromItemForMask(v.JoinExpr.Rarg, out)
		}
	case *pg.Node_RangeSubselect:
		if rv, ok := s.wrappers[v.RangeSubselect]; ok {
			recordTable(rv, out, s.defaultCatalog)
		}
	}
}
