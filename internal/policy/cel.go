// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// celEnv is the shared CEL environment. It declares the variables every
// policy expression may reference: subject.*, request.*, and query.*.
// There is deliberately no `now` variable — a time-varying input would
// make the rewrite cache (internal/policycache) key unsound.
var (
	celEnvOnce sync.Once
	celEnv     *cel.Env
	celEnvErr  error

	filterEnvOnce sync.Once
	filterEnv     *cel.Env
	filterEnvErr  error
)

func env() (*cel.Env, error) {
	celEnvOnce.Do(func() {
		celEnv, celEnvErr = cel.NewEnv(
			cel.Variable("subject", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("query", cel.MapType(cel.StringType, cel.DynType)),
		)
	})
	return celEnv, celEnvErr
}

// rowFilterEnv adds the `row` variable for row-filter expressions. These
// are lowered to SQL predicates (never evaluated in-process), so row is a
// compile-time-only symbol whose field selections become column refs.
func rowFilterEnv() (*cel.Env, error) {
	filterEnvOnce.Do(func() {
		filterEnv, filterEnvErr = cel.NewEnv(
			cel.Variable("subject", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
			cel.Variable("row", cel.MapType(cel.StringType, cel.DynType)),
		)
	})
	return filterEnv, filterEnvErr
}

// celProgram aliases cel.Program so other files in the package can hold a
// compiled program without importing cel-go directly.
type celProgram = cel.Program

// CompiledCondition is a named boolean CEL program attached to a policy.
type CompiledCondition struct {
	Name string
	Prog cel.Program
}

// compileConditions compiles each condition into a bool CEL program.
// A non-bool result type or any parse/check error fails compilation — the
// same fail-closed posture the MVP had when it rejected conditions
// outright, so `sluice policy validate` still catches bad expressions at
// load time.
func compileConditions(cs []apitypes.Condition) ([]CompiledCondition, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	e, err := env()
	if err != nil {
		return nil, fmt.Errorf("cel env: %w", err)
	}
	out := make([]CompiledCondition, 0, len(cs))
	for _, c := range cs {
		if c.Expression == "" {
			return nil, fmt.Errorf("condition %q: expression is required", c.Name)
		}
		prog, err := compileBoolProgram(e, c.Expression)
		if err != nil {
			return nil, fmt.Errorf("condition %q: %w", c.Name, err)
		}
		out = append(out, CompiledCondition{Name: c.Name, Prog: prog})
	}
	return out, nil
}

// compileBoolProgram parses, type-checks (requiring a bool result), and
// plans a CEL expression.
func compileBoolProgram(e *cel.Env, expr string) (cel.Program, error) {
	ast, iss := e.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrConditionInvalid, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("%w: expression must return bool, got %s", ErrConditionInvalid, ast.OutputType())
	}
	prog, err := e.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConditionInvalid, err)
	}
	return prog, nil
}

// activation builds the CEL variable bindings for one evaluation.
func activation(in Input) map[string]any {
	return map[string]any{
		"subject": subjectVars(in.User),
		"request": requestVars(in.Request),
		"query":   queryVars(in.Shape, in.Tables),
	}
}

func subjectVars(u *identity.UserCtx) map[string]any {
	if u == nil {
		return map[string]any{
			"id": "", "issuer": "", "email": "",
			"groups": []string{}, "auth_method": "", "claims": map[string]any{},
		}
	}
	claims := u.Claims
	if claims == nil {
		claims = map[string]any{}
	}
	return map[string]any{
		"id":          u.Subject,
		"issuer":      u.Issuer,
		"email":       u.Email,
		"groups":      u.Groups,
		"auth_method": string(u.AuthMethod),
		"claims":      claims,
	}
}

func requestVars(f *RequestFacts) map[string]any {
	if f == nil {
		return map[string]any{"remote_ip": "", "user_agent": "", "headers": map[string]string{}}
	}
	ip := ""
	if f.RemoteIP != nil {
		ip = f.RemoteIP.String()
	}
	headers := f.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return map[string]any{"remote_ip": ip, "user_agent": f.UserAgent, "headers": headers}
}

func queryVars(shape parser.QueryShape, tables []parser.TableRef) map[string]any {
	names := make([]string, 0, len(tables))
	for _, t := range tables {
		names = append(names, tableKey(t))
	}
	return map[string]any{
		"has_select_star":  shape.HasSelectStar,
		"is_aggregate":     shape.IsAggregate,
		"has_cte":          shape.HasCTE,
		"has_union":        shape.HasUnion,
		"has_limit":        shape.HasLimit,
		"limit":            shape.LimitValue,
		"has_order_by":     shape.HasOrderBy,
		"has_where":        shape.HasWhere,
		"where_columns":    shape.WhereColumns,
		"group_by_columns": shape.GroupByColumns,
		"joins":            shape.Joins,
		"catalogs":         shape.Catalogs,
		"is_recursive_cte": shape.IsRecursiveCTE,
		"tables":           names,
	}
}

// evalBool runs a bool CEL program against the activation. A runtime
// error or a non-bool result is surfaced to the caller, which treats it
// as a hard failure (deny) — never as "condition false".
func evalBool(prog cel.Program, act map[string]any) (bool, error) {
	out, _, err := prog.Eval(act)
	if err != nil {
		return false, err
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("condition did not evaluate to bool")
	}
	return b, nil
}
