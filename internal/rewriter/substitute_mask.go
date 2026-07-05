// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"fmt"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
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
	for _, f := range sel.FromClause {
		recordFromItemForMask(f, aliases, s.defaultCatalog)
	}

	// Recurse into CTE bodies and subqueries first.
	if sel.WithClause != nil {
		for _, cte := range sel.WithClause.Ctes {
			if cte == nil {
				continue
			}
			if c, ok := cte.Node.(*pg.Node_CommonTableExpr); ok && c.CommonTableExpr != nil {
				if err := s.maskInNode(c.CommonTableExpr.Ctequery, aliases); err != nil {
					return err
				}
			}
		}
	}
	for _, f := range sel.FromClause {
		if rs, ok := f.Node.(*pg.Node_RangeSubselect); ok && rs.RangeSubselect != nil {
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

	// TargetList.
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
			continue
		}
		// Preserve column alias so the client sees the expected name.
		if rt.ResTarget.Name == "" && origName != "" {
			rt.ResTarget.Name = origName
		}
		rt.ResTarget.Val = newVal
		sel.TargetList[i] = t
	}

	// WHERE, HAVING, GROUP BY, ORDER BY expressions.
	if sel.WhereClause != nil {
		if err := s.maskInExpr(sel.WhereClause, aliases); err != nil {
			return err
		}
	}
	if sel.HavingClause != nil {
		if err := s.maskInExpr(sel.HavingClause, aliases); err != nil {
			return err
		}
	}
	for _, g := range sel.GroupClause {
		if err := s.maskInExpr(g, aliases); err != nil {
			return err
		}
	}
	for _, o := range sel.SortClause {
		if err := s.maskInExpr(o, aliases); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) maskInJoin(j *pg.JoinExpr, aliases aliasMap) error {
	if j == nil {
		return nil
	}
	if rs, ok := j.Larg.GetNode().(*pg.Node_RangeSubselect); ok && rs.RangeSubselect != nil {
		if err := s.maskInNode(rs.RangeSubselect.Subquery, aliases); err != nil {
			return err
		}
	}
	if rs, ok := j.Rarg.GetNode().(*pg.Node_RangeSubselect); ok && rs.RangeSubselect != nil {
		if err := s.maskInNode(rs.RangeSubselect.Subquery, aliases); err != nil {
			return err
		}
	}
	if j.Quals != nil {
		return s.maskInExpr(j.Quals, aliases)
	}
	return nil
}

// maskInExpr performs in-place substitution anywhere inside an
// expression tree. It replaces the inner ColumnRef with the mask
// expression via a parent-slot update.
func (s *state) maskInExpr(n *pg.Node, aliases aliasMap) error {
	if n == nil {
		return nil
	}
	switch v := n.Node.(type) {
	case *pg.Node_AExpr:
		if v.AExpr == nil {
			return nil
		}
		if replaced, _, err := s.maybeSubstituteColumnRef(v.AExpr.Lexpr, aliases); err != nil {
			return err
		} else if replaced != nil {
			v.AExpr.Lexpr = replaced
		} else if err := s.maskInExpr(v.AExpr.Lexpr, aliases); err != nil {
			return err
		}
		if replaced, _, err := s.maybeSubstituteColumnRef(v.AExpr.Rexpr, aliases); err != nil {
			return err
		} else if replaced != nil {
			v.AExpr.Rexpr = replaced
		} else if err := s.maskInExpr(v.AExpr.Rexpr, aliases); err != nil {
			return err
		}
	case *pg.Node_BoolExpr:
		if v.BoolExpr == nil {
			return nil
		}
		for i, a := range v.BoolExpr.Args {
			if replaced, _, err := s.maybeSubstituteColumnRef(a, aliases); err != nil {
				return err
			} else if replaced != nil {
				v.BoolExpr.Args[i] = replaced
				continue
			}
			if err := s.maskInExpr(a, aliases); err != nil {
				return err
			}
		}
	case *pg.Node_SubLink:
		if v.SubLink != nil {
			return s.maskInNode(v.SubLink.Subselect, aliases)
		}
	}
	return nil
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
	mask, tableKey, ok := s.lookupMaskForColumn(aliases, qualifier, column)
	if !ok {
		return nil, "", nil
	}
	expr, err := s.buildMaskExpr(mask, tableKey, column, n)
	if err != nil {
		return nil, "", fmt.Errorf("column %s.%s: %w", tableKey, column, err)
	}
	s.rewrites = append(s.rewrites, "column-mask:"+tableKey+"."+column)
	return expr, column, nil
}

// lookupMaskForColumn returns the mask registered for the column under
// the resolved TableRef. A qualifier matches alias-exact; if no
// qualifier was given we walk every aliased table looking for a unique
// hit. Mask column selectors may be wildcard patterns ("*", "ssn_*"), so
// resolution matches the actual column against each mask's pattern rather
// than doing an exact key lookup.
func (s *state) lookupMaskForColumn(aliases aliasMap, qualifier, column string) (*policy.CompiledMask, string, bool) {
	if qualifier != "" {
		ref, ok := aliases[qualifier]
		if !ok {
			return nil, "", false
		}
		tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
		if m, ok := s.maskForResolvedColumn(tk, column); ok {
			return m, tk, true
		}
		return nil, "", false
	}
	var hit *policy.CompiledMask
	var hitKey string
	for _, ref := range aliases {
		tk := ref.Catalog + "." + ref.Schema + "." + ref.Table
		m, ok := s.maskForResolvedColumn(tk, column)
		if !ok {
			continue
		}
		if hit != nil && hitKey != tk {
			// Ambiguous — two distinct tables in scope both mask this
			// column. DuckDB will error on ambiguity; we let the user see
			// that error rather than pick.
			return nil, "", false
		}
		hit = m
		hitKey = tk
	}
	if hit == nil {
		return nil, "", false
	}
	return hit, hitKey, true
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

// recordFromItemForMask is a lighter-weight variant of recordFromItem
// that treats a RangeSubselect originating from row-filter wrapping as
// its underlying table — the mask layer needs to resolve the original
// TableKey, not the synthetic subquery.
func recordFromItemForMask(n *pg.Node, out aliasMap, defaultCatalog string) {
	if n == nil {
		return
	}
	switch v := n.Node.(type) {
	case *pg.Node_RangeVar:
		recordFromItem(n, out, defaultCatalog)
	case *pg.Node_JoinExpr:
		if v.JoinExpr == nil {
			return
		}
		recordFromItemForMask(v.JoinExpr.Larg, out, defaultCatalog)
		recordFromItemForMask(v.JoinExpr.Rarg, out, defaultCatalog)
	case *pg.Node_RangeSubselect:
		if v.RangeSubselect == nil {
			return
		}
		// Extract the inner RangeVar, if any, and register it under the
		// RangeSubselect's alias.
		alias := ""
		if v.RangeSubselect.Alias != nil {
			alias = v.RangeSubselect.Alias.Aliasname
		}
		if sel, ok := v.RangeSubselect.Subquery.GetNode().(*pg.Node_SelectStmt); ok && sel.SelectStmt != nil {
			for _, f := range sel.SelectStmt.FromClause {
				if rv, ok := f.Node.(*pg.Node_RangeVar); ok && rv.RangeVar != nil {
					catalog := rv.RangeVar.Catalogname
					if catalog == "" {
						catalog = defaultCatalog
					}
					key := alias
					if key == "" {
						key = rv.RangeVar.Relname
					}
					out[key] = parser.TableRef{
						Catalog: catalog,
						Schema:  rv.RangeVar.Schemaname,
						Table:   rv.RangeVar.Relname,
						Alias:   alias,
					}
					return
				}
			}
		}
	}
}
