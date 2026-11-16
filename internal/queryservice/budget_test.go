// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// fakeBudget implements the queryservice budgetGate.
type fakeBudget struct {
	blockAfter int64
	rows       int64
	recorded   int64
	cpu        time.Duration
}

func (b *fakeBudget) Check(_ context.Context, _, _ string) error {
	if b.blockAfter > 0 && b.rows >= b.blockAfter {
		return pkgerr.New(pkgerr.CodeBudgetExceeded).WithMessage("over budget")
	}
	return nil
}

func (b *fakeBudget) Record(_, _ string, cpu time.Duration, rows int64) {
	b.cpu += cpu
	b.rows += rows
	b.recorded++
}

func newBudgetService(t *testing.T, b queryservice.Options) *queryservice.Service {
	t.Helper()
	b.Parser = &fakeParser{}
	b.Policy = &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}}
	b.Rewriter = &fakeRewriter{}
	b.Audit = &fakeAudit{}
	b.Clock = func() time.Time { return time.Unix(1713600000, 0) }
	return queryservice.New(b)
}

func TestBudget_RecordsAndBlocks(t *testing.T) {
	fb := &fakeBudget{blockAfter: 2}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}, rows: [][]any{{int64(1)}, {int64(2)}}}
	svc := newBudgetService(t, queryservice.Options{Budget: fb, Executor: ex})

	user := &identity.UserCtx{Subject: "alice"}
	// First query: under budget, executes, records 2 rows.
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t", User: user})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	for res.Rows.Next() {
		var v any
		_ = res.Rows.Scan(&v)
	}
	_ = res.Rows.Close() // triggers Record

	if fb.recorded != 1 {
		t.Errorf("Record called %d times, want 1", fb.recorded)
	}

	// Second query: now over budget (2 >= 2) → refused pre-admission.
	_, err = svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t", User: user})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeBudgetExceeded {
		t.Fatalf("second execute err = %v, want ERR_BUDGET_EXCEEDED", err)
	}
}

func TestBudget_NoUserSkipsBudget(t *testing.T) {
	fb := &fakeBudget{blockAfter: 1}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
	svc := newBudgetService(t, queryservice.Options{Budget: fb, Executor: ex})
	// Anonymous request: no budget subject, so Check is skipped.
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t"})
	if err != nil {
		t.Fatalf("anonymous execute: %v", err)
	}
	_ = res.Rows.Close()
	if fb.recorded != 0 {
		t.Errorf("budget recorded for anonymous request")
	}
}

// steppingClock advances by step on every read.
func steppingClock(step time.Duration) func() time.Time {
	var mu sync.Mutex
	now := time.Unix(1713600000, 0)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(step)
		return now
	}
}

// A query that fails, for example on a timeout, is charged its wall time
// with 0 rows. Before, only a query that served rows was charged.
func TestBudget_FailedQueryIsCharged(t *testing.T) {
	fb := &fakeBudget{}
	ex := &fakeExecutor{err: fmt.Errorf("%w: %w", executor.ErrExecute, context.DeadlineExceeded)}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    &fakeAudit{},
		Budget:   fb,
		Clock:    steppingClock(time.Second),
	})
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t", User: &identity.UserCtx{Subject: "alice"}})
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeTimeout {
		t.Fatalf("err = %v, want ERR_TIMEOUT", err)
	}
	if fb.recorded != 1 || fb.rows != 0 || fb.cpu <= 0 {
		t.Fatalf("Record calls %d, rows %d, cpu %s; want one call with 0 rows and the wall time", fb.recorded, fb.rows, fb.cpu)
	}
}

// A served query is charged the executor's run time, not the time since
// the request started.
func TestBudget_ChargesExecutorTime(t *testing.T) {
	fb := &fakeBudget{}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}, rows: [][]any{{int64(1)}}, execTime: 1500 * time.Millisecond}
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{},
		Executor: ex,
		Audit:    &fakeAudit{},
		Budget:   fb,
		Clock:    steppingClock(time.Minute),
	})
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t", User: &identity.UserCtx{Subject: "alice"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for res.Rows.Next() {
		var v any
		_ = res.Rows.Scan(&v)
	}
	_ = res.Rows.Close()
	if fb.recorded != 1 || fb.rows != 1 || fb.cpu != 1500*time.Millisecond {
		t.Fatalf("Record calls %d, rows %d, cpu %s; want one call with 1 row and 1.5s", fb.recorded, fb.rows, fb.cpu)
	}
}
