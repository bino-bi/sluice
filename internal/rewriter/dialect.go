// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/bino-bi/sluice/internal/parser"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// ParseChecker returns DuckDB's parse trees (json_serialize_sql) of two
// statements. A statement DuckDB cannot parse yields a tree that starts
// with {"error":true.
type ParseChecker interface {
	ParseTrees(ctx context.Context, a, b string) (treeA, treeB string, err error)
}

// normalizeForDuckDB rewrites, in place, caller node shapes that pg_query
// deparses into text DuckDB reads in another way. src is the text tree was
// parsed from. The parse check guards all rules but the alias case: a wrong
// rewrite makes the trees differ and the statement is refused. The check
// compares alias names without regard to case, so the alias case rule must
// be right on its own.
func normalizeForDuckDB(tree *pg.ParseResult, src string) {
	var names aliasNames
	_ = walkTree(tree.ProtoReflect(), func(m protoreflect.Message) (bool, error) {
		switch v := m.Interface().(type) {
		case *pg.Node:
			if repl := duckDBNode(v); repl != nil {
				v.Node = repl.Node
			}
		case *pg.TypeName:
			normalizeTypeName(v, src)
		case *pg.ResTarget:
			names.def(&v.Name)
		case *pg.Alias:
			names.def(&v.Aliasname)
			names.defList(v.Colnames)
		case *pg.CommonTableExpr:
			names.defList(v.Aliascolnames)
		case *pg.NamedArgExpr:
			names.def(&v.Name)
		case *pg.ColumnRef:
			names.ref(v.Location, len(v.Fields))
		case *pg.FuncCall:
			restoreExtractUnit(v, src)
			names.ref(v.Location, len(v.Funcname))
		case *pg.RangeVar:
			names.ref(v.Location, 3)
		}
		return true, nil
	})
	names.restoreCase(src)
}

// duckDBNode returns the node DuckDB's parser builds for n, or nil when n
// needs no change.
func duckDBNode(n *pg.Node) *pg.Node {
	switch v := n.Node.(type) {
	case *pg.Node_AExpr:
		return likeEscapeCall(v.AExpr)
	case *pg.Node_TypeCast:
		return intervalLiteralCall(v.TypeCast)
	}
	return nil
}

// likeEscapeFuncs maps the operator of x [NOT] [I]LIKE p ESCAPE e to the
// function DuckDB reads it as. pg_query reads it as x <op>
// pg_catalog.like_escape(p, e), which DuckDB has no 2-argument form of.
var likeEscapeFuncs = map[string]string{
	"~~":   "like_escape",
	"!~~":  "not_like_escape",
	"~~*":  "ilike_escape",
	"!~~*": "not_ilike_escape",
}

func likeEscapeCall(e *pg.A_Expr) *pg.Node {
	if (e.Kind != pg.A_Expr_Kind_AEXPR_LIKE && e.Kind != pg.A_Expr_Kind_AEXPR_ILIKE) || len(e.Name) != 1 {
		return nil
	}
	fn, ok := likeEscapeFuncs[e.Name[0].GetString_().GetSval()]
	call := e.Rexpr.GetFuncCall()
	// The call pg_query builds for ESCAPE has the location of the operator.
	// A like_escape call the caller wrote has its own and stays.
	if !ok || call == nil || call.Location != e.Location || len(call.Args) != 2 ||
		!isQualifiedName(call.Funcname, "pg_catalog", "like_escape") {
		return nil
	}
	return pg.MakeFuncCallNode([]*pg.Node{pg.MakeStrNode(fn)}, []*pg.Node{e.Lexpr, call.Args[0], call.Args[1]}, e.Location)
}

// intervalUnits maps the typmod of INTERVAL 'n' <unit> to the function
// DuckDB expands the literal to, and the type it truncates n to ("" for
// none). pg_query keeps the unit as a typmod, which DuckDB ignores on a
// cast: '400'::interval day is 400 seconds there.
var intervalUnits = map[int32]struct{ fn, typ string }{
	4:    {"to_years", "int4"},
	2:    {"to_months", "int4"},
	8:    {"to_days", "int4"},
	1024: {"to_hours", "int8"},
	2048: {"to_minutes", "int8"},
	4096: {"to_seconds", ""},
}

// intervalLiteralCall rewrites the typed literal INTERVAL 'n' <unit>. Only
// the literal has no location: DuckDB reads an explicit cast such as
// '1'::interval day as pg_query deparses it.
func intervalLiteralCall(c *pg.TypeCast) *pg.Node {
	tn := c.TypeName
	if c.Location != -1 || tn == nil || !isQualifiedName(tn.Names, "pg_catalog", "interval") ||
		len(tn.Typmods) != 1 || len(tn.ArrayBounds) != 0 || c.Arg.GetAConst().GetSval() == nil {
		return nil
	}
	unit, ok := intervalUnits[tn.Typmods[0].GetAConst().GetIval().GetIval()]
	if !ok {
		return nil
	}
	n := castTo(c.Arg, "float8")
	if unit.typ != "" {
		n = castTo(pg.MakeFuncCallNode([]*pg.Node{pg.MakeStrNode("trunc")}, []*pg.Node{n}, -1), unit.typ)
	}
	return pg.MakeFuncCallNode([]*pg.Node{pg.MakeStrNode(unit.fn)}, []*pg.Node{n}, -1)
}

// castTo casts n to the pg_catalog type typ. The location stays -1, so the
// FLOAT rule does not read the source text for it.
func castTo(n *pg.Node, typ string) *pg.Node {
	return &pg.Node{Node: &pg.Node_TypeCast{TypeCast: &pg.TypeCast{
		Arg: n,
		TypeName: &pg.TypeName{
			Names:    []*pg.Node{pg.MakeStrNode("pg_catalog"), pg.MakeStrNode(typ)},
			Typemod:  -1,
			Location: -1,
		},
		Location: -1,
	}}}
}

func normalizeTypeName(tn *pg.TypeName, src string) {
	if len(tn.Names) != 2 || tn.Names[0].GetString_().GetSval() != "pg_catalog" {
		return
	}
	switch tn.Names[1].GetString_().GetSval() {
	case "json", "bit", "varbit":
		// DuckDB writes these types without the pg_catalog schema. It has
		// no varbit type, so that one fails at bind as the raw text does.
		tn.Names = tn.Names[1:]
	case "float8":
		// pg_query reads a bare FLOAT as float8, DuckDB as REAL.
		if len(tn.Typmods) == 0 && isBareFloat(src, tn.Location) {
			tn.Names[1] = pg.MakeStrNode("float4")
		}
	}
}

// isBareFloat reports whether src has the word FLOAT at loc, without a
// precision after it. FLOAT(n) is resolved by both parsers alike.
func isBareFloat(src string, loc int32) bool {
	const word = "float"
	if loc < 0 || int(loc)+len(word) > len(src) || !equalFoldASCII(src[loc:int(loc)+len(word)], word) {
		return false
	}
	rest := src[int(loc)+len(word):]
	if rest != "" && isIdentByte(rest[0]) {
		return false
	}
	return !strings.HasPrefix(strings.TrimLeft(rest, " \t\n\r\f\v"), "(")
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// duckDBExtractKeywords are the EXTRACT units DuckDB's grammar has as
// keywords. It writes them as the lower-case singular unit.
var duckDBExtractKeywords = map[string]bool{
	"year": true, "years": true, "month": true, "months": true, "day": true, "days": true,
	"hour": true, "hours": true, "minute": true, "minutes": true, "second": true, "seconds": true,
	"millisecond": true, "milliseconds": true, "microsecond": true, "microseconds": true,
	"week": true, "weeks": true, "quarter": true, "quarters": true, "decade": true, "decades": true,
	"century": true, "centuries": true, "millennium": true, "millennia": true,
}

// restoreExtractUnit gives the unit of EXTRACT(<unit> FROM x) the spelling
// of src. pg_query folds an identifier unit to lower case. DuckDB keeps its
// spelling in the date_part constant unless the unit is one of its
// keywords: EXTRACT(EPOCH FROM ts) is date_part('EPOCH', ts) there.
func restoreExtractUnit(fc *pg.FuncCall, src string) {
	if fc.Funcformat != pg.CoercionForm_COERCE_SQL_SYNTAX || len(fc.Args) != 2 ||
		!isQualifiedName(fc.Funcname, "pg_catalog", "extract") {
		return
	}
	c := fc.Args[0].GetAConst()
	unit := c.GetSval()
	if unit == nil || c.Location < 0 || duckDBExtractKeywords[unit.Sval] {
		return
	}
	// A quoted unit starts with a quote, so it never folds to unit.Sval.
	start, end := int(c.Location), int(c.Location)+len(unit.Sval)
	if end > len(src) || !equalFoldASCII(src[start:end], unit.Sval) || (end < len(src) && isIdentByte(src[end])) {
		return
	}
	unit.Sval = src[start:end]
}

func isQualifiedName(names []*pg.Node, schema, name string) bool {
	return len(names) == 2 && names[0].GetString_().GetSval() == schema && names[1].GetString_().GetSval() == name
}

// aliasNames holds the alias names of a tree, and where in the source it
// names a column, function or table.
type aliasNames struct {
	defs []*string
	refs []nameRef
}

// nameRef is a dotted name of at most parts parts that starts at loc.
type nameRef struct {
	loc   int32
	parts int
}

func (a *aliasNames) def(p *string) {
	if *p != "" {
		a.defs = append(a.defs, p)
	}
}

func (a *aliasNames) defList(nodes []*pg.Node) {
	for _, n := range nodes {
		if s := n.GetString_(); s != nil {
			a.def(&s.Sval)
		}
	}
}

func (a *aliasNames) ref(loc int32, parts int) {
	if loc >= 0 {
		a.refs = append(a.refs, nameRef{loc, parts})
	}
}

// restoreCase gives the alias names pg_query folded to lower case back the
// spelling of src. DuckDB keeps the spelling of an alias, and it becomes
// data: a result column name, a struct key, a to_json key. Tokens the tree
// shows to be column, function or table names are skipped. A name is only
// restored when every other token of src that yields it is spelled the same
// way, so a definition written in lower case keeps its case. Otherwise it
// stays lower case, as before.
func (a *aliasNames) restoreCase(src string) {
	if len(a.defs) == 0 || !hasUpperASCII(src) {
		return
	}
	scan, err := pg.Scan(src)
	if err != nil {
		return
	}
	toks := scan.GetTokens()
	isRef := make([]bool, len(toks))
	for _, r := range a.refs {
		i, ok := slices.BinarySearchFunc(toks, r.loc, func(t *pg.ScanToken, loc int32) int { return cmp.Compare(t.Start, loc) })
		for k := 0; ok && k < r.parts; k++ {
			isRef[i] = true
			ok = i+2 < len(toks) && toks[i+1].Token == pg.Token_ASCII_46
			i += 2
		}
	}
	spelling := map[string]string{}
	ambiguous := map[string]bool{}
	for i, tok := range toks {
		if isRef[i] || int(tok.End) > len(src) || tok.Start >= tok.End {
			continue
		}
		text := src[tok.Start:tok.End]
		var name string
		switch {
		case tok.Token == pg.Token_UIDENT:
			// Its decoded name is not in src.
			return
		case tok.Token == pg.Token_IDENT && text[0] == '"':
			text = strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
			name = text
		case tok.Token == pg.Token_IDENT, tok.KeywordKind != pg.KeywordKind_NO_KEYWORD:
			name = parser.FoldASCII(text)
		default:
			continue
		}
		if prev, ok := spelling[name]; ok && prev != text {
			ambiguous[name] = true
		}
		spelling[name] = text
	}
	for _, p := range a.defs {
		if s, ok := spelling[*p]; ok && !ambiguous[*p] {
			*p = s
		}
	}
}

func hasUpperASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if 'A' <= s[i] && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// checkDuckDBParse refuses a statement when DuckDB reads the deparse of
// tree in another way than the caller's text. tree is the normalized
// clone before any mutation. json_serialize_sql serializes SELECT only.
func (r *Rewriter) checkDuckDBParse(ctx context.Context, req RewriteRequest, tree *pg.ParseResult) error {
	if r.opts.ParseCheck == nil || req.AST.Statement() != parser.StmtSelect {
		return nil
	}
	same, rawParses, err := duckDBVerdict(ctx, r.opts.ParseCheck, req.Raw, tree)
	switch {
	case err != nil:
		return err
	case !rawParses:
		return pkgerr.New(pkgerr.CodeSyntax).WithMessage("DuckDB cannot parse the statement")
	case !same:
		return pkgerr.New(pkgerr.CodeUnsupportedSyntax).
			WithMessage("the statement would change meaning when rewritten for row filters, masks or limits; see docs/policies/row-filters.md")
	}
	return nil
}

// duckDBVerdict reports whether DuckDB reads the deparse of tree as src,
// and whether it can parse src. A check that ends with the context returns
// ctx.Err().
func duckDBVerdict(ctx context.Context, pc ParseChecker, src string, tree *pg.ParseResult) (same, srcParses bool, err error) {
	dep, err := pg.Deparse(tree)
	if err != nil {
		return false, false, fmt.Errorf("%w: %w", ErrDeparseFailed, err)
	}
	srcTree, depTree, err := pc.ParseTrees(ctx, src, dep)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, false, ctxErr
		}
		return false, false, fmt.Errorf("rewriter: duckdb parse check: %w", err)
	}
	same, srcParses = sameDuckDBParse(srcTree, depTree)
	return same, srcParses, nil
}

// errorTreePrefix starts the json_serialize_sql output of a statement
// DuckDB cannot parse.
const errorTreePrefix = `{"error":true`

// queryLocationKey holds a byte offset into the statement text.
const queryLocationKey = `"query_location":`

// sameDuckDBParse reports whether two json_serialize_sql trees describe
// the same statement, and whether the first one parsed. It walks both in
// one pass and:
//   - skips query locations;
//   - reads the schema "main" as no schema, because DuckDB qualifies the
//     like_escape it expands LIKE ... ESCAPE to;
//   - compares a string constant ("value") exactly;
//   - compares everything else without regard to ASCII case, as DuckDB
//     resolves names.
func sameDuckDBParse(rawJSON, depJSON string) (same, rawParses bool) {
	if strings.HasPrefix(rawJSON, errorTreePrefix) {
		return false, false
	}
	if strings.HasPrefix(depJSON, errorTreePrefix) {
		return false, true
	}
	a, b := rawJSON, depJSON
	i, j := 0, 0
	for {
		// i and j are outside a string here.
		i, j = skipQueryLocation(a, i), skipQueryLocation(b, j)
		if i == len(a) || j == len(b) {
			return i == len(a) && j == len(b), true
		}
		if a[i] != '"' || b[j] != '"' {
			if lowerASCII(a[i]) != lowerASCII(b[j]) {
				return false, true
			}
			i++
			j++
			continue
		}
		ei, ej := jsonStringEnd(a, i), jsonStringEnd(b, j)
		if ei < 0 || ej < 0 || !equalFoldASCII(a[i:ei], b[j:ej]) {
			return false, true
		}
		key := a[i:ei]
		i, j = ei, ej
		if key != `"value"` && key != `"schema"` {
			continue
		}
		// Only a key followed by a string value gets the special rules.
		if !strings.HasPrefix(a[i:], `:"`) || !strings.HasPrefix(b[j:], `:"`) {
			continue
		}
		i++
		j++
		ei, ej = jsonStringEnd(a, i), jsonStringEnd(b, j)
		if ei < 0 || ej < 0 {
			return false, true
		}
		va, vb := a[i:ei], b[j:ej]
		if key == `"value"` {
			if va != vb {
				return false, true
			}
		} else if !equalFoldASCII(mainAsNoSchema(va), mainAsNoSchema(vb)) {
			return false, true
		}
		i, j = ei, ej
	}
}

// skipQueryLocation returns the index after a query_location member at
// s[i:], or i when there is none.
func skipQueryLocation(s string, i int) int {
	if !strings.HasPrefix(s[i:], queryLocationKey) {
		return i
	}
	i += len(queryLocationKey)
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == ',' {
		i++
	}
	return i
}

// jsonStringEnd returns the index after the JSON string that starts at
// s[i], or -1 when it is not closed.
func jsonStringEnd(s string, i int) int {
	for k := i + 1; k < len(s); k++ {
		switch s[k] {
		case '\\':
			k++
		case '"':
			return k + 1
		}
	}
	return -1
}

func mainAsNoSchema(quoted string) string {
	if equalFoldASCII(quoted, `"main"`) {
		return `""`
	}
	return quoted
}

// equalFoldASCII folds A-Z only. strings.EqualFold would also match the
// Kelvin sign with k, which DuckDB does not.
func equalFoldASCII(x, y string) bool {
	if len(x) != len(y) {
		return false
	}
	for k := 0; k < len(x); k++ {
		if lowerASCII(x[k]) != lowerASCII(y[k]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
