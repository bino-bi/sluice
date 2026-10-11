// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

type funcChecker func(ctx context.Context, a, b string) (string, string, error)

func (f funcChecker) ParseTrees(ctx context.Context, a, b string) (string, string, error) {
	return f(ctx, a, b)
}

// A failing DuckDB parse check is a server fault, a check that runs out of
// memory means the statement is too large, and a context that ends during
// the check is a cancel or a timeout. The audit record carries the client
// code.
func TestExecute_ParseCheckErrorCodes(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		check   func(ctx context.Context, cancel context.CancelFunc) error
		code    pkgerr.Code
	}{
		{"checker failure", 0, func(context.Context, context.CancelFunc) error {
			return errors.New("parse instance gone")
		}, pkgerr.CodeInternal},
		// executor.ParseTrees returns its out-of-memory error like this.
		{"out of memory", 0, func(context.Context, context.CancelFunc) error {
			return pkgerr.Wrap(pkgerr.CodePayloadTooLarge, executor.ErrOutOfMemory).WithMessage("send a shorter statement")
		}, pkgerr.CodePayloadTooLarge},
		{"canceled", 0, func(ctx context.Context, cancel context.CancelFunc) error {
			cancel()
			return ctx.Err()
		}, pkgerr.CodeCanceled},
		{"deadline", 50 * time.Millisecond, func(ctx context.Context, _ context.CancelFunc) error {
			<-ctx.Done()
			return ctx.Err()
		}, pkgerr.CodeTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if tc.timeout > 0 {
				ctx, cancel = context.WithTimeout(context.Background(), tc.timeout)
			}
			defer cancel()

			p := pgquery.New(parser.Options{})
			a := &fakeAudit{}
			ex := &fakeExecutor{}
			svc := queryservice.New(queryservice.Options{
				Parser: p,
				Policy: &fakePolicy{decision: &policy.Decision{
					Outcome: policy.OutcomeAllow,
					Rewrite: &policy.RewriteEffect{LimitMax: 5},
				}},
				Rewriter: rewriter.New(rewriter.Options{
					Parser: p,
					ParseCheck: funcChecker(func(ctx context.Context, _, _ string) (string, string, error) {
						return "", "", tc.check(ctx, cancel)
					}),
				}),
				Executor: ex,
				Audit:    a,
			})

			_, err := svc.Execute(ctx, queryservice.QueryRequest{SQL: "SELECT id FROM t"})
			ae := pkgerr.FromError(err)
			if ae == nil || ae.Code != tc.code {
				t.Fatalf("client code = %v, want %s", err, tc.code)
			}
			if tc.code == pkgerr.CodePayloadTooLarge && !strings.Contains(ae.Message, "shorter statement") {
				t.Errorf("message = %q, want a hint", ae.Message)
			}
			if ex.execCount != 0 {
				t.Errorf("executor ran %d times", ex.execCount)
			}
			recs := a.all()
			if len(recs) != 1 {
				t.Fatalf("want 1 audit record, got %d", len(recs))
			}
			if recs[0].Decision != audit.DecisionError || recs[0].ErrorCode != string(tc.code) {
				t.Errorf("audit decision %s, error_code %q, want error and %s", recs[0].Decision, recs[0].ErrorCode, tc.code)
			}
		})
	}
}
