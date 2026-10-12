// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"errors"
	"fmt"
	"maps"
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

// applyPostMasks plans the post-query masks (FPE, fake, jitter, hmac):
// bare top-level select items are recorded for Go-side masking. The table
// wrapper passes such a column through raw, so the guard walk then refuses
// every use of it that planPostMasks cannot follow. It runs before the
// tables are wrapped and changes nothing.
func (s *state) applyPostMasks(raw *pg.ParseResult) error {
	if raw == nil || !s.hasPostQueryMasks() {
		return nil
	}
	if err := s.planPostMasks(raw); err != nil {
		return err
	}
	// planPostMasks only passes a statement that is one plain SELECT.
	return s.guardSelect(raw.Stmts[0].Stmt.GetSelectStmt(), nil)
}

func (s *state) guardSelect(sel *pg.SelectStmt, outerAliases aliasMap) error {
	if sel == nil {
		return nil
	}

	aliases := aliasMap{}
	for alias, ref := range outerAliases {
		aliases[alias] = ref
	}
	local := aliasMap{}
	for _, f := range sel.FromClause {
		s.recordFromItem(f, local)
	}
	for alias, ref := range local {
		// DuckDB falls back to the outer table when the inner one lacks the
		// column, the lookup below does not.
		if outer, ok := outerAliases[alias]; ok {
			outerKey := outer.Catalog + "." + outer.Schema + "." + outer.Table
			if outerKey != ref.Catalog+"."+ref.Schema+"."+ref.Table && s.tableHasPostMask(outerKey) {
				return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"the name %q hides an outer table with post-query masked columns, give it another alias", alias))
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
				if err := s.guardSelect(c.CommonTableExpr.Ctequery.GetSelectStmt(), outerAliases); err != nil {
					return err
				}
			}
		}
	}
	for _, f := range sel.FromClause {
		if rs, ok := f.Node.(*pg.Node_RangeSubselect); ok && rs.RangeSubselect != nil {
			if err := s.guardSelect(rs.RangeSubselect.Subquery.GetSelectStmt(), aliases); err != nil {
				return err
			}
		}
		if j, ok := f.Node.(*pg.Node_JoinExpr); ok && j.JoinExpr != nil {
			if err := s.guardJoin(j.JoinExpr, aliases); err != nil {
				return err
			}
		}
	}
	if sel.Larg != nil {
		if err := s.guardSelect(sel.Larg, aliases); err != nil {
			return err
		}
	}
	if sel.Rarg != nil {
		if err := s.guardSelect(sel.Rarg, aliases); err != nil {
			return err
		}
	}

	outputNames := map[string]bool{}
	for _, t := range sel.TargetList {
		if name := t.GetResTarget().GetName(); name != "" {
			outputNames[foldName(name)] = true
		}
	}

	// Every expression-bearing clause. VALUES rows too: `(VALUES (c.name))`
	// in a subquery reads the outer column.
	for _, list := range [][]*pg.Node{
		sel.TargetList, {sel.WhereClause, sel.HavingClause, sel.LimitCount, sel.LimitOffset},
		sel.GroupClause, sel.DistinctClause, sel.WindowClause, sel.ValuesLists,
	} {
		for _, n := range list {
			if err := s.guardExpr(n, aliases); err != nil {
				return err
			}
		}
	}
	for _, sb := range sel.SortClause {
		// `count(*) AS customers ... ORDER BY customers` sorts by the
		// output column, not by the table of that name.
		if name := loneColumnName(sb.GetSortBy().GetNode()); name != "" && outputNames[name] {
			// An ambiguous name is fine here too: the output column it
			// sorts by is checked as a select item.
			if _, _, masked, err := s.lookupMaskForColumn(aliases, "", name); err != nil || !masked {
				continue
			}
		}
		if err := s.guardExpr(sb, aliases); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) guardJoin(j *pg.JoinExpr, aliases aliasMap) error {
	if j == nil {
		return nil
	}
	for _, arg := range []*pg.Node{j.Larg, j.Rarg} {
		switch v := arg.GetNode().(type) {
		case *pg.Node_RangeSubselect:
			if v.RangeSubselect != nil {
				if err := s.guardSelect(v.RangeSubselect.Subquery.GetSelectStmt(), aliases); err != nil {
					return err
				}
			}
		case *pg.Node_JoinExpr:
			if err := s.guardJoin(v.JoinExpr, aliases); err != nil {
				return err
			}
		}
	}
	return s.guardExpr(j.Quals, aliases)
}

// guardExpr checks every column reference in an expression of the current
// SELECT. A nested SELECT is checked in its own scope. The walk is by
// reflection, so no expression context can hide a reference.
func (s *state) guardExpr(n *pg.Node, aliases aliasMap) error {
	if n == nil {
		return nil
	}
	return walkTree(n.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.SelectStmt:
			return false, s.guardSelect(v, aliases)
		case *pg.ColumnRef:
			return false, s.guardColumnRef(v, aliases)
		case *pg.SQLValueFunction:
			cr := &pg.ColumnRef{Fields: []*pg.Node{pg.MakeStrNode(sqlValueName(v))}}
			return false, s.guardColumnRef(cr, aliases)
		}
		return true, nil
	})
}

// guardColumnRef refuses a reference that hands a post-query masked value
// on raw: the masked column in a nested SELECT, which returns it to its
// parent under a name planPostMasks cannot trace, or the whole row or a
// field of a table with such a column.
func (s *state) guardColumnRef(cr *pg.ColumnRef, aliases aliasMap) error {
	qualifier, column := splitColumnRef(cr)
	if column == "" {
		return nil
	}
	_, _, ok, err := s.lookupMaskForColumn(aliases, qualifier, column)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.checkWholeRowRef(cr, aliases); err != nil {
			return err
		}
		return s.checkFieldAccess(cr, aliases)
	}
	if s.selectDepth > 1 {
		if qualifier == "" {
			return pkgerr.Wrap(pkgerr.CodeMaskContext, ErrMaskPostQueryContext).WithMessage(fmt.Sprintf(
				"column %q is post-query masked in a table in scope and cannot be used in a nested SELECT; to read the column of another table, qualify it with the table name", column))
		}
		return ErrMaskPostQueryContext
	}
	return nil
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

// checkWholeRowRef refuses a reference whose last name is the alias or
// name of a table with a post-query mask: the alias alone (`c`) or behind
// a schema or catalog (`main.customers`). DuckDB returns the whole row as
// a struct for it, with the masked column raw, also in to_json(c),
// (c).name or c['name']. A column of that name in scope is no exception:
// which of the two DuckDB binds depends on the clause.
func (s *state) checkWholeRowRef(cr *pg.ColumnRef, aliases aliasMap) error {
	n := len(cr.Fields)
	if n == 0 || n > 3 {
		return nil
	}
	name := foldName(cr.Fields[n-1].GetString_().GetSval())
	ref, ok := aliases[name]
	if !ok || !s.tableHasPostMask(ref.Catalog+"."+ref.Schema+"."+ref.Table) {
		return nil
	}
	return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
		"%q names a table with post-query masked columns and cannot be used as a value or column name; select the table's columns by name, or give the table another alias", name))
}

// checkFieldAccess refuses dotted access into a post-query masked column
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
		_, _, masked, err := s.lookupMaskForColumn(scope, qualifier, column)
		if err == nil && !masked {
			continue
		}
		// A wildcard mask matches any name, also a table alias. Only a
		// real column counts.
		if !s.columnInScope(column, scope) {
			continue
		}
		return pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
			"field access on post-query masked column %q is not supported", column))
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
// Without a schema cache entry this cannot be shown, so the answer is no.
func (s *state) columnInScope(name string, aliases aliasMap) bool {
	for _, ref := range aliases {
		entry, err := s.tableEntry(ref.Catalog + "." + ref.Schema + "." + ref.Table)
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

// checkPostMaskForms runs when the decision has a post-query mask. It
// refuses forms that give a column another name or position: a column
// alias list on a table (`t AS x(a, b)`), a join alias (`(a JOIN b ON
// ...) AS j`), two FROM items under one name, a star inside an
// expression, `#N`, the `alias.` prefix and a table called alias. The guard
// walk and star expansion resolve a column by its own name under the
// table's alias and cannot follow them. The two alias rules cover every
// table, masked or not: star expansion lists a table by its cached column
// names. A CTE reference counts as a table here. Then every qualifier must
// be a FROM item (checkQualifiers). It must run before the tables are
// wrapped.
func (s *state) checkPostMaskForms(raw *pg.ParseResult) error {
	if raw == nil || !s.hasPostQueryMasks() {
		return nil
	}
	err := walkTree(raw.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.RangeVar:
			if len(v.GetAlias().GetColnames()) > 0 {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"a column alias list on a table or CTE reference is not supported with post-query masks; refer to the columns by their own names, or name the columns of a CTE in its definition (WITH q(a, b) AS ...)")
			}
			// Star expansion writes alias.<col> for such a table. See the
			// alias. prefix rule below.
			if aliasKey(v) == "alias" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					`a table called "alias" is not supported with post-query masks, give it another alias`)
			}
		case *pg.JoinExpr:
			if v.Alias != nil || v.JoinUsingAlias != nil {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"a join alias is not supported with post-query masks, refer to the columns by their own names")
			}
		case *pg.SelectStmt:
			// Two FROM items under one name: the guard walk can only keep
			// one of them, and the other table's masks would be skipped.
			if dup := duplicateFromName(v.FromClause, map[string]bool{}); dup != "" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"the name %q is used for more than one FROM item, give each its own alias", dup))
			}
		case *pg.ResTarget:
			// A star that is a select item itself is left to star expansion.
			if isStarRef(v.GetVal().GetColumnRef()) {
				return false, nil
			}
		case *pg.ColumnRef:
			// DuckDB reads `c.* LIKE 'email'` as the columns of c that match.
			if isStarRef(v) {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"a star inside an expression is not supported with post-query masks, list the columns")
			}
			// DuckDB reads `alias.n` as the select alias n, also when a FROM
			// item is called alias and has no column n. That reads a bound
			// post-query masked item raw.
			if len(v.Fields) > 1 && foldName(v.Fields[0].GetString_().GetSval()) == "alias" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"the alias. prefix is not supported with post-query masks")
			}
		case *pg.A_Expr:
			// DuckDB reads `#3` as the third column of the FROM list.
			if v.Lexpr == nil && len(v.Name) == 1 && v.Name[0].GetString_().GetSval() == "#" {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(
					"positional column references (#N) are not supported with post-query masks")
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	return eachSubquery(raw.ProtoReflect(), func(sel *pg.SelectStmt) error {
		return checkQualifiers(sel, nil)
	})
}

// checkQualifiers refuses a column reference with two or more parts whose
// first part is not a FROM item of its SELECT or of a SELECT around it.
// DuckDB resolves such a qualifier in ways the guard walk does not follow:
// a schema or catalog in front of a table, a column in front of a STRUCT
// field. outer holds the names of the SELECTs around sel. The walk is by
// reflection, so no clause can hide a reference.
func checkQualifiers(sel *pg.SelectStmt, outer map[string]bool) error {
	names := map[string]bool{}
	maps.Copy(names, outer)
	for _, f := range sel.GetFromClause() {
		for _, item := range appendFromEntries(nil, f, aliasMap{}, "") {
			names[item.alias] = true
		}
	}
	return walkTree(sel.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.SelectStmt:
			if v != sel {
				return false, checkQualifiers(v, names)
			}
		case *pg.CommonTableExpr:
			// A CTE body cannot see the FROM items of the SELECT that defines it.
			return false, checkQualifiers(v.GetCtequery().GetSelectStmt(), outer)
		case *pg.ColumnRef:
			if len(v.Fields) < 2 {
				return false, nil
			}
			if q := foldName(v.Fields[0].GetString_().GetSval()); q == "" || !names[q] {
				return false, pkgerr.New(pkgerr.CodeMaskContext).WithMessage(fmt.Sprintf(
					"%q is not a table or alias of the FROM clause; with post-query masks qualify columns with a table alias, and write (col).field for a STRUCT field", q))
			}
			return false, nil
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

// lacksColumn reports whether the schema cache shows that the table has no
// column of that name. Unknown counts as "may have it".
func (s *state) lacksColumn(ref parser.TableRef, column string) bool {
	entry, err := s.tableEntry(ref.Catalog + "." + ref.Schema + "." + ref.Table)
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

// tableEntry returns the schema cache entry of a table key. The first
// answer is kept for the whole Rewrite, so star expansion, the post-mask
// checks and the wrapper all use one column list, even when another
// request reloads the catalog in between.
func (s *state) tableEntry(key string) (*schema.Entry, error) {
	if r, ok := s.tables[key]; ok {
		return r.entry, r.err
	}
	entry, err := s.schemaEntry(key)
	if err != nil {
		err = fmt.Errorf("%w: %s: %w", ErrSchemaMissing, key, err)
	}
	if s.tables == nil {
		s.tables = map[string]tableLookup{}
	}
	s.tables[key] = tableLookup{entry: entry, err: err}
	return entry, err
}

// schemaEntry looks a table up in the schema cache without regard to
// case. Keys in this package are lower case, the cache keeps the names the
// source reports.
func (s *state) schemaEntry(key string) (*schema.Entry, error) {
	if s.schema == nil {
		return nil, errors.New("schema cache not configured")
	}
	folded := strings.ToLower(key)
	if s.schemaIndex == nil {
		s.indexSchema()
	}
	if e, ok := s.schemaIndex[folded]; ok {
		return e, nil
	}
	// Not cached under any case: Get loads the catalog.
	catalog, schemaName, table := splitTableKey(key)
	entry, err := s.schema.Get(s.ctx, schema.Key{Catalog: catalog, Schema: schemaName, Table: table})
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

// lookupMaskForColumn returns the post-query mask that wins for the column
// under the resolved TableRef. A SQL mask is applied in the table wrapper,
// so a column it wins is not a hit. A qualifier matches alias-exact; if no
// qualifier was given we walk every aliased table looking for a unique
// hit. An inner table that has a column of the same name does not hide an
// outer hit: where DuckDB binds the name depends on the clause. Mask
// column selectors may be wildcard patterns ("*", "ssn_*"), so resolution
// matches the actual column against each mask's pattern rather than doing
// an exact key lookup.
func (s *state) lookupMaskForColumn(aliases aliasMap, qualifier, column string) (*policy.CompiledMask, string, bool, error) {
	if qualifier != "" {
		ref, ok := aliases[foldName(qualifier)]
		if !ok {
			return nil, "", false, nil
		}
		tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
		if m, ok := s.maskForResolvedColumn(tk, column); ok && s.isPostQueryMask(m) {
			return m, tk, true, nil
		}
		return nil, "", false, nil
	}
	// The tables of the current SELECT are asked first, so a hit there and
	// one in an outer SELECT are not ambiguous. Any hit counts.
	for _, scope := range []aliasMap{s.localAliases, aliases} {
		var hit *policy.CompiledMask
		var hitKey string
		for _, ref := range scope {
			tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
			m, ok := s.maskForResolvedColumn(tk, column)
			if !ok || !s.isPostQueryMask(m) {
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
					"column %q is post-query masked in more than one table in scope, qualify it with the table name", column))
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

// buildMaskExpr returns the expression the wrapper projects for a masked
// column; orig is the column reference it reads. null and constant keep
// their literal fast paths; every other type goes through the provider's
// MaskSQL snippet, parsed and spliced around a clone of orig.
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

// recordFromItem registers the tables of one FROM item under the
// names a query can use to qualify their columns. A subquery the caller
// wrote is not registered: its columns are checked in its own SELECT, and
// a column it derives under the name of a masked column is not that
// column.
func (s *state) recordFromItem(n *pg.Node, out aliasMap) {
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
			s.recordFromItem(v.JoinExpr.Larg, out)
			s.recordFromItem(v.JoinExpr.Rarg, out)
		}
	}
}
