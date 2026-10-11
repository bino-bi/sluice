// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"

	"github.com/bino-bi/sluice/internal/identity"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// renderMaskSnippet parses a provider's MaskSQL snippet into an AST
// expression, splices the original column reference in place of every
// __col__ placeholder, renumbers the provider-local $1..$n placeholders
// into the overall query parameter list, and appends the provider's
// params to state.params. The snippet text comes exclusively from the
// provider (never from Args), so parsing it is safe; anything
// user-influencable arrives as a bound parameter. pkg/mask is public, so
// the snippet gets the same DuckDB normalization and parse check as the
// statement.
func (s *state) renderMaskSnippet(snippet string, params []pkgmask.Param, orig *pg.Node) (*pg.Node, error) {
	src := "SELECT " + snippet
	parsed, err := pg.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: snippet does not parse: %w", ErrMaskUnsupported, err)
	}
	if len(parsed.Stmts) != 1 {
		return nil, fmt.Errorf("%w: snippet must be a single expression", ErrMaskUnsupported)
	}
	normalizeForDuckDB(parsed, src)
	sel := parsed.Stmts[0].Stmt.GetSelectStmt()
	if sel == nil || len(sel.TargetList) != 1 {
		return nil, fmt.Errorf("%w: snippet must be a single scalar expression", ErrMaskUnsupported)
	}
	rt := sel.TargetList[0].GetResTarget()
	if rt == nil || rt.Val == nil {
		return nil, fmt.Errorf("%w: snippet has no expression", ErrMaskUnsupported)
	}
	if !isAtomicSnippet(rt.Val) {
		return nil, fmt.Errorf("%w: the top node of the snippet must be a function call, CASE, COALESCE, GREATEST, LEAST, NULLIF, cast, column, constant or parameter; wrap an operator such as || or AT TIME ZONE in coalesce(...)", ErrMaskUnsupported)
	}
	if err := s.snippets.check(s.ctx, src, parsed); err != nil {
		return nil, err
	}

	base := len(s.params)
	expr, err := spliceSnippet(rt.Val, orig, base, len(params))
	if err != nil {
		return nil, err
	}
	for _, p := range params {
		s.params = append(s.params, p.Value)
	}
	return expr, nil
}

// syntaxCallsInParens are the pg_catalog functions pg_query writes in SQL
// syntax inside their own parentheses, such as TRIM(BOTH FROM x).
var syntaxCallsInParens = map[string]bool{
	"btrim": true, "ltrim": true, "rtrim": true, "extract": true,
	"substring": true, "position": true, "overlay": true, "normalize": true,
}

// isAtomicSnippet reports whether the deparse writes n as one unit, so n
// keeps its meaning in any expression the snippet is spliced into. The
// parse check only sees the snippet on its own. Other functions in SQL
// syntax are not one unit: x AT TIME ZONE 'UTC' || 'a' loses its parentheses.
func isAtomicSnippet(n *pg.Node) bool {
	switch v := n.Node.(type) {
	case *pg.Node_FuncCall:
		fc := v.FuncCall
		return fc.Funcformat == pg.CoercionForm_COERCE_EXPLICIT_CALL ||
			len(fc.Funcname) == 2 && fc.Funcname[0].GetString_().GetSval() == "pg_catalog" &&
				syntaxCallsInParens[fc.Funcname[1].GetString_().GetSval()]
	case *pg.Node_AExpr:
		return v.AExpr.Kind == pg.A_Expr_Kind_AEXPR_NULLIF
	case *pg.Node_CaseExpr, *pg.Node_CoalesceExpr, *pg.Node_MinMaxExpr, *pg.Node_TypeCast,
		*pg.Node_ColumnRef, *pg.Node_AConst, *pg.Node_ParamRef:
		return true
	}
	return false
}

// maxSnippetMemo bounds the texts a snippetMemo keeps. A provider that puts
// request data into its snippet text would grow it without end; past the
// bound, each new text is checked every time.
const maxSnippetMemo = 4096

// snippetMemo caches the DuckDB parse check verdict of each mask snippet
// text. The verdict depends on the text alone. A nil pc skips the check.
type snippetMemo struct {
	pc       ParseChecker
	verdicts sync.Map // snippet text -> error, nil when DuckDB reads it as written
	n        atomic.Int64
}

// check refuses a snippet when DuckDB reads its deparse in another way than
// src. parsed is the normalized parse of src, before the splice. A failed
// check is not cached.
func (m *snippetMemo) check(ctx context.Context, src string, parsed *pg.ParseResult) error {
	if m.pc == nil {
		return nil
	}
	if v, ok := m.verdicts.Load(src); ok {
		err, _ := v.(error)
		return err
	}
	same, srcParses, err := duckDBVerdict(ctx, m.pc, src, parsed)
	if err != nil {
		return err
	}
	var verdict error
	switch {
	case !srcParses:
		verdict = fmt.Errorf("%w: DuckDB cannot parse the mask snippet", ErrMaskUnsupported)
	case !same:
		verdict = fmt.Errorf("%w: mask snippet changes meaning in DuckDB", ErrMaskUnsupported)
	}
	if m.n.Load() < maxSnippetMemo {
		if _, loaded := m.verdicts.LoadOrStore(src, verdict); !loaded {
			m.n.Add(1)
		}
	}
	return verdict
}

// spliceSnippet walks the snippet expression tree, replacing __col__
// column references with a clone of orig and shifting ParamRef numbers by
// base. The walker handles exactly the node types a scalar mask
// expression can contain; anything else fails loudly so a provider using
// unsupported syntax is caught by its round-trip test, not in production.
func spliceSnippet(n *pg.Node, orig *pg.Node, base, np int) (*pg.Node, error) {
	if n == nil || n.Node == nil {
		return n, nil
	}
	recurse := func(child *pg.Node) (*pg.Node, error) {
		return spliceSnippet(child, orig, base, np)
	}
	switch v := n.Node.(type) {
	case *pg.Node_ColumnRef:
		if isColPlaceholder(v.ColumnRef) {
			cl, ok := proto.Clone(orig).(*pg.Node)
			if !ok {
				return nil, fmt.Errorf("%w: cannot clone column reference", ErrMaskUnsupported)
			}
			return cl, nil
		}
		return nil, fmt.Errorf("%w: snippet references column %v (only __col__ is allowed)", ErrMaskUnsupported, v.ColumnRef.Fields)
	case *pg.Node_ParamRef:
		k := int(v.ParamRef.Number)
		if k < 1 || k > np {
			return nil, fmt.Errorf("%w: snippet references $%d but provider returned %d params", ErrMaskUnsupported, k, np)
		}
		v.ParamRef.Number = int32(base + k)
		return n, nil
	case *pg.Node_AConst:
		return n, nil
	case *pg.Node_TypeCast:
		repl, err := recurse(v.TypeCast.Arg)
		if err != nil {
			return nil, err
		}
		v.TypeCast.Arg = repl
		return n, nil
	case *pg.Node_FuncCall:
		for i, a := range v.FuncCall.Args {
			repl, err := recurse(a)
			if err != nil {
				return nil, err
			}
			v.FuncCall.Args[i] = repl
		}
		return n, nil
	case *pg.Node_MinMaxExpr: // greatest() / least()
		for i, a := range v.MinMaxExpr.Args {
			repl, err := recurse(a)
			if err != nil {
				return nil, err
			}
			v.MinMaxExpr.Args[i] = repl
		}
		return n, nil
	case *pg.Node_CoalesceExpr:
		for i, a := range v.CoalesceExpr.Args {
			repl, err := recurse(a)
			if err != nil {
				return nil, err
			}
			v.CoalesceExpr.Args[i] = repl
		}
		return n, nil
	case *pg.Node_CaseExpr:
		if v.CaseExpr.Arg != nil {
			repl, err := recurse(v.CaseExpr.Arg)
			if err != nil {
				return nil, err
			}
			v.CaseExpr.Arg = repl
		}
		for i, w := range v.CaseExpr.Args {
			repl, err := recurse(w)
			if err != nil {
				return nil, err
			}
			v.CaseExpr.Args[i] = repl
		}
		if v.CaseExpr.Defresult != nil {
			repl, err := recurse(v.CaseExpr.Defresult)
			if err != nil {
				return nil, err
			}
			v.CaseExpr.Defresult = repl
		}
		return n, nil
	case *pg.Node_CaseWhen:
		repl, err := recurse(v.CaseWhen.Expr)
		if err != nil {
			return nil, err
		}
		v.CaseWhen.Expr = repl
		repl, err = recurse(v.CaseWhen.Result)
		if err != nil {
			return nil, err
		}
		v.CaseWhen.Result = repl
		return n, nil
	case *pg.Node_AExpr:
		if v.AExpr.Lexpr != nil {
			repl, err := recurse(v.AExpr.Lexpr)
			if err != nil {
				return nil, err
			}
			v.AExpr.Lexpr = repl
		}
		if v.AExpr.Rexpr != nil {
			repl, err := recurse(v.AExpr.Rexpr)
			if err != nil {
				return nil, err
			}
			v.AExpr.Rexpr = repl
		}
		return n, nil
	case *pg.Node_BoolExpr:
		for i, a := range v.BoolExpr.Args {
			repl, err := recurse(a)
			if err != nil {
				return nil, err
			}
			v.BoolExpr.Args[i] = repl
		}
		return n, nil
	case *pg.Node_NullTest:
		repl, err := recurse(v.NullTest.Arg)
		if err != nil {
			return nil, err
		}
		v.NullTest.Arg = repl
		return n, nil
	default:
		return nil, fmt.Errorf("%w: unsupported node %T in mask snippet", ErrMaskUnsupported, n.Node)
	}
}

// isColPlaceholder reports whether cr is the bare __col__ marker.
func isColPlaceholder(cr *pg.ColumnRef) bool {
	if cr == nil || len(cr.Fields) != 1 {
		return false
	}
	sNode, ok := cr.Fields[0].Node.(*pg.Node_String_)
	return ok && sNode.String_ != nil && sNode.String_.Sval == "__col__"
}

// maskIdentity adapts identity.UserCtx to the read-only pkg/mask view.
type maskIdentity struct{ u *identity.UserCtx }

func (m maskIdentity) Subject() string {
	if m.u == nil {
		return ""
	}
	return m.u.Subject
}

func (m maskIdentity) Groups() []string {
	if m.u == nil {
		return nil
	}
	return m.u.Groups
}

func (m maskIdentity) Claim(name string) (any, bool) {
	if m.u == nil || m.u.Claims == nil {
		return nil, false
	}
	v, ok := m.u.Claims[name]
	return v, ok
}
