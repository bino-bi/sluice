// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types/ref"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// compileFilterExpression translates a CEL row-filter expression into the
// existing CompiledPredicate tree, so the rewriter's proven parameterised
// SQL emission (positional $N params, subject/request templates) is reused
// unchanged — CEL never renders SQL text. The supported subset:
//
//   - row.<col> compared to a literal or subject/request template via
//     == != < <= > >=
//   - && || ! for boolean composition
//   - row.<col> in [a, b, ...] (literals only)
//   - row.<col>.startsWith/endsWith/contains(<string literal>)
//
// Anything outside the subset (arithmetic, macros, function calls,
// query.* references, dynamic Like arguments) fails compilation, matching
// the load-time validation contract of `sluice policy validate`.
func compileFilterExpression(expr string) (*CompiledPredicate, error) {
	e, err := rowFilterEnv()
	if err != nil {
		return nil, fmt.Errorf("cel env: %w", err)
	}
	checked, iss := e.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrFilterExprInvalid, iss.Err())
	}
	if checked.OutputType().String() != "bool" {
		return nil, fmt.Errorf("%w: expression must return bool, got %s", ErrFilterExprInvalid, checked.OutputType())
	}
	return lowerExpr(checked.NativeRep().Expr())
}

func lowerExpr(e ast.Expr) (*CompiledPredicate, error) {
	if e.Kind() != ast.CallKind {
		return nil, fmt.Errorf("%w: expected a comparison or boolean operator", ErrFilterExprInvalid)
	}
	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	switch fn {
	case operators.LogicalAnd:
		return lowerBoolOp(args, false)
	case operators.LogicalOr:
		return lowerBoolOp(args, true)
	case operators.LogicalNot:
		inner, err := lowerExpr(args[0])
		if err != nil {
			return nil, err
		}
		return &CompiledPredicate{Not: inner}, nil
	case operators.Equals:
		return lowerComparison(call, apitypes.PredOpEquals)
	case operators.NotEquals:
		return lowerComparison(call, apitypes.PredOpNotEquals)
	case operators.Less:
		return lowerComparison(call, apitypes.PredOpLessThan)
	case operators.LessEquals:
		return lowerComparison(call, apitypes.PredOpLessThanOrEqual)
	case operators.Greater:
		return lowerComparison(call, apitypes.PredOpGreaterThan)
	case operators.GreaterEquals:
		return lowerComparison(call, apitypes.PredOpGreaterThanOrEqual)
	case operators.In, operators.OldIn:
		return lowerIn(args)
	case "startsWith":
		return lowerStringMatch(call, "%s%%")
	case "endsWith":
		return lowerStringMatch(call, "%%%s")
	case "contains":
		return lowerStringMatch(call, "%%%s%%")
	default:
		return nil, fmt.Errorf("%w: unsupported operator %q", ErrFilterExprInvalid, fn)
	}
}

func lowerBoolOp(args []ast.Expr, or bool) (*CompiledPredicate, error) {
	out := &CompiledPredicate{}
	for _, a := range args {
		c, err := lowerExpr(a)
		if err != nil {
			return nil, err
		}
		if or {
			out.Any = append(out.Any, c)
		} else {
			out.All = append(out.All, c)
		}
	}
	return out, nil
}

// lowerComparison expects one operand to be a row.<col> select and the
// other a literal or subject/request template. Operand order is
// normalised so the column is always on the left.
func lowerComparison(call ast.CallExpr, op apitypes.PredOp) (*CompiledPredicate, error) {
	args := call.Args()
	if len(args) != 2 {
		return nil, fmt.Errorf("%w: %s needs two operands", ErrFilterExprInvalid, op)
	}
	lcol, lok := rowColumn(args[0])
	rcol, rok := rowColumn(args[1])
	switch {
	case lok && !rok:
		vs, err := valueOperand(args[1])
		if err != nil {
			return nil, err
		}
		return &CompiledPredicate{Column: lcol, Op: op, Values: []ValueSource{vs}}, nil
	case rok && !lok:
		vs, err := valueOperand(args[0])
		if err != nil {
			return nil, err
		}
		return &CompiledPredicate{Column: rcol, Op: flipOp(op), Values: []ValueSource{vs}}, nil
	default:
		return nil, fmt.Errorf("%w: exactly one operand must be row.<column>", ErrFilterExprInvalid)
	}
}

func lowerIn(args []ast.Expr) (*CompiledPredicate, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%w: 'in' needs two operands", ErrFilterExprInvalid)
	}
	col, ok := rowColumn(args[0])
	if !ok {
		return nil, fmt.Errorf("%w: 'in' left operand must be row.<column>", ErrFilterExprInvalid)
	}
	if args[1].Kind() != ast.ListKind {
		return nil, fmt.Errorf("%w: 'in' right operand must be a literal list", ErrFilterExprInvalid)
	}
	list := args[1].AsList()
	vals := make([]ValueSource, 0, list.Size())
	for _, el := range list.Elements() {
		vs, err := valueOperand(el)
		if err != nil {
			return nil, err
		}
		vals = append(vals, vs)
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("%w: 'in' list is empty", ErrFilterExprInvalid)
	}
	return &CompiledPredicate{Column: col, Op: apitypes.PredOpIn, Values: vals}, nil
}

// lowerStringMatch handles row.col.startsWith/endsWith/contains(<literal>)
// by translating to a Like predicate with the literal escaped and wrapped
// per pattern. Only a string-literal argument is allowed — a dynamic
// argument cannot be safely escaped at compile time.
func lowerStringMatch(call ast.CallExpr, pattern string) (*CompiledPredicate, error) {
	col, ok := rowColumn(call.Target())
	if !ok {
		return nil, fmt.Errorf("%w: string matcher must target row.<column>", ErrFilterExprInvalid)
	}
	args := call.Args()
	if len(args) != 1 || args[0].Kind() != ast.LiteralKind {
		return nil, fmt.Errorf("%w: string matcher requires a single string literal argument", ErrFilterExprInvalid)
	}
	s, ok := literalString(args[0].AsLiteral())
	if !ok {
		return nil, fmt.Errorf("%w: string matcher argument must be a string literal", ErrFilterExprInvalid)
	}
	like := fmt.Sprintf(pattern, escapeLike(s))
	return &CompiledPredicate{Column: col, Op: apitypes.PredOpLike, Values: []ValueSource{{Literal: like}}}, nil
}

// rowColumn returns the column name if e is a `row.<col>` selection.
func rowColumn(e ast.Expr) (string, bool) {
	if e == nil || e.Kind() != ast.SelectKind {
		return "", false
	}
	sel := e.AsSelect()
	operand := sel.Operand()
	if operand.Kind() != ast.IdentKind || operand.AsIdent() != "row" {
		return "", false
	}
	return sel.FieldName(), true
}

// valueOperand converts a literal or subject/request template selection
// into a ValueSource. Literals bind as parameters; subject/request paths
// become Templates resolved per request.
func valueOperand(e ast.Expr) (ValueSource, error) {
	switch e.Kind() {
	case ast.LiteralKind:
		return ValueSource{Literal: refToGo(e.AsLiteral())}, nil
	case ast.SelectKind, ast.IdentKind:
		path, ok := dottedPath(e)
		if !ok {
			return ValueSource{}, fmt.Errorf("%w: unsupported value reference", ErrFilterExprInvalid)
		}
		if len(path) == 0 || (path[0] != "subject" && path[0] != "request") {
			return ValueSource{}, fmt.Errorf("%w: value reference must be a literal, subject.*, or request.*", ErrFilterExprInvalid)
		}
		return ValueSource{Template: &Template{Path: path, Raw: strings.Join(path, ".")}}, nil
	default:
		return ValueSource{}, fmt.Errorf("%w: unsupported value expression", ErrFilterExprInvalid)
	}
}

// dottedPath renders a nested Select/Ident chain into a path slice,
// mapping the CEL "claims"/"id" leaves onto the template vocabulary.
func dottedPath(e ast.Expr) ([]string, bool) {
	var parts []string
	for {
		switch e.Kind() {
		case ast.IdentKind:
			parts = append([]string{e.AsIdent()}, parts...)
			return normalizeTemplatePath(parts), true
		case ast.SelectKind:
			sel := e.AsSelect()
			parts = append([]string{sel.FieldName()}, parts...)
			e = sel.Operand()
		default:
			return nil, false
		}
	}
}

// normalizeTemplatePath rewrites the CEL activation names into the paths
// the Template renderer understands: subject.id -> subject.sub, and
// subject.claims.x -> subject.claims.x (already aligned).
func normalizeTemplatePath(path []string) []string {
	if len(path) >= 2 && path[0] == "subject" && path[1] == "id" {
		return append([]string{"subject", "sub"}, path[2:]...)
	}
	return path
}

func flipOp(op apitypes.PredOp) apitypes.PredOp {
	switch op {
	case apitypes.PredOpLessThan:
		return apitypes.PredOpGreaterThan
	case apitypes.PredOpLessThanOrEqual:
		return apitypes.PredOpGreaterThanOrEqual
	case apitypes.PredOpGreaterThan:
		return apitypes.PredOpLessThan
	case apitypes.PredOpGreaterThanOrEqual:
		return apitypes.PredOpLessThanOrEqual
	default:
		return op // Equals / NotEquals are symmetric
	}
}

func refToGo(v ref.Val) any {
	if v == nil {
		return nil
	}
	return v.Value()
}

func literalString(v ref.Val) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.Value().(string)
	return s, ok
}

// escapeLike escapes LIKE metacharacters so a startsWith/contains literal
// matches literally.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
