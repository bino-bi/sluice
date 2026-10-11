// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// fakeChecker records each call and answers with fn.
type fakeChecker struct {
	calls [][2]string
	fn    func(ctx context.Context, a, b string) (string, string, error)
}

func (f *fakeChecker) ParseTrees(ctx context.Context, a, b string) (string, string, error) {
	f.calls = append(f.calls, [2]string{a, b})
	return f.fn(ctx, a, b)
}

func trees(a, b string) func(context.Context, string, string) (string, string, error) {
	return func(context.Context, string, string) (string, string, error) { return a, b, nil }
}

func checkedRewriter(fc *fakeChecker) *rewriter.Rewriter {
	return rewriter.New(rewriter.Options{
		Parser:         pgquery.New(parser.Options{}),
		DefaultCatalog: "pg",
		ParseCheck:     fc,
	})
}

// filtered returns a rewriter and a request for sql under a tenant filter
// on orders.
func filtered(t *testing.T, fc *fakeChecker, sql string) (*rewriter.Rewriter, rewriter.RewriteRequest) {
	t.Helper()
	user := &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}
	dec, ast := evaluate(t, sql, user, allowAll(0), tenantFilter("orders"))
	return checkedRewriter(fc), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql}
}

// The check compares the caller's text with the deparse of the clone
// before any policy mutation.
func TestRewrite_ParseCheckInputs(t *testing.T) {
	fc := &fakeChecker{fn: trees(`{"error":false}`, `{"error":false}`)}
	sql := "SELECT ID FROM pg.public.orders WHERE total > 1"
	rw, req := filtered(t, fc, sql)
	res, err := rw.Rewrite(context.Background(), req)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "tenant_id") {
		t.Errorf("SQL = %q, want the filter", res.SQL)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("checker calls = %d, want 1", len(fc.calls))
	}
	if got := fc.calls[0]; got[0] != sql || got[1] != "SELECT id FROM pg.public.orders WHERE total > 1" {
		t.Errorf("checker got %q", got)
	}
}

func TestRewrite_ParseCheckVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		raw, dep string
		code     pkgerr.Code
		message  string
	}{
		{"trees differ", `{"error":false,"a":1}`, `{"error":false,"a":2}`, pkgerr.CodeUnsupportedSyntax, "docs/policies/row-filters.md"},
		{"raw does not parse", `{"error":true,"error_type":"parser"}`, `{"error":false}`, pkgerr.CodeSyntax, "DuckDB cannot parse"},
		{"deparse does not parse", `{"error":false}`, `{"error":true,"error_type":"parser"}`, pkgerr.CodeUnsupportedSyntax, "change meaning"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeChecker{fn: trees(tc.raw, tc.dep)}
			rw, req := filtered(t, fc, "SELECT id FROM pg.public.orders")
			res, err := rw.Rewrite(context.Background(), req)
			if err == nil {
				t.Fatalf("served %q", res.SQL)
			}
			var ae *pkgerr.APIError
			if !errors.As(err, &ae) || ae.Code != tc.code || !strings.Contains(ae.Message, tc.message) {
				t.Errorf("err = %v, want %s with %q", err, tc.code, tc.message)
			}
		})
	}
}

// A checker failure is not the caller's fault: it carries no sentinel and
// no client code, so queryservice reports ERR_INTERNAL.
func TestRewrite_ParseCheckFailure(t *testing.T) {
	boom := errors.New("parse instance gone")
	fc := &fakeChecker{fn: func(context.Context, string, string) (string, string, error) { return "", "", boom }}
	rw, req := filtered(t, fc, "SELECT id FROM pg.public.orders")
	_, err := rw.Rewrite(context.Background(), req)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the checker error", err)
	}
	var ae *pkgerr.APIError
	if errors.As(err, &ae) {
		t.Errorf("err carries client code %s", ae.Code)
	}
	for _, s := range []error{rewriter.ErrUnsupportedSyntax, rewriter.ErrDeparseFailed, rewriter.ErrMaskPostQueryContext, rewriter.ErrStatementRejected} {
		if errors.Is(err, s) {
			t.Errorf("err matches sentinel %v", s)
		}
	}
}

func TestRewrite_ParseCheckContextErrors(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithTimeout(context.Background(), 0)
	defer cancel2()

	cases := []struct {
		name string
		ctx  context.Context
		want error
		fn   func(ctx context.Context, a, b string) (string, string, error)
	}{
		{"canceled", canceled, context.Canceled, func(ctx context.Context, _, _ string) (string, string, error) {
			return "", "", fmt.Errorf("executor: canceled: %w", ctx.Err())
		}},
		{"deadline", expired, context.DeadlineExceeded, func(ctx context.Context, _, _ string) (string, string, error) {
			return "", "", fmt.Errorf("executor: query execution failed: %w", ctx.Err())
		}},
		// The driver may report an interrupt without wrapping ctx.Err().
		{"canceled, other error", canceled, context.Canceled, func(context.Context, string, string) (string, string, error) {
			return "", "", errors.New("interrupted")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeChecker{fn: tc.fn}
			rw, req := filtered(t, fc, "SELECT id FROM pg.public.orders")
			_, err := rw.Rewrite(tc.ctx, req)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Pass-through and statements json_serialize_sql cannot serialize skip
// the check.
func TestRewrite_ParseCheckSkipped(t *testing.T) {
	fc := &fakeChecker{fn: trees(`{"error":true}`, `{"error":true}`)}
	rw := checkedRewriter(fc)
	p := pgquery.New(parser.Options{})

	sql := "SELECT id FROM pg.public.orders"
	dec, ast := evaluate(t, sql, nil, allowAll(0))
	if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql}); err != nil {
		t.Errorf("pass-through: %v", err)
	}

	limit := &policy.Decision{Outcome: policy.OutcomeAllow, Rewrite: &policy.RewriteEffect{LimitMax: 5}}
	for _, sql := range []string{"EXPLAIN SELECT id FROM pg.public.orders", "SHOW TABLES"} {
		ast, err := p.Parse(context.Background(), sql)
		if err != nil {
			t.Fatalf("parse %s: %v", sql, err)
		}
		if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: limit, Raw: sql}); err != nil {
			t.Errorf("%s with a limit: %v", sql, err)
		}
	}
	if len(fc.calls) != 0 {
		t.Errorf("checker called for %q", fc.calls)
	}

	// A SELECT under the same limit is checked.
	ast, err := p.Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: limit, Raw: sql}); err == nil {
		t.Error("limited SELECT skipped the check")
	}
}
