// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// countingRewriter records how many times Rewrite is called.
type countingRewriter struct {
	calls atomic.Int64
}

func (r *countingRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	r.calls.Add(1)
	return &rewriter.RewriteResult{SQL: req.Raw, Fingerprint: "fp-out"}, nil
}

// countingPolicy records how many times Evaluate is called and reports a
// stable snapshot identity so the cache is active.
type countingPolicy struct {
	calls atomic.Int64
	dec   *policy.Decision
}

func (p *countingPolicy) Evaluate(_ context.Context, _ policy.Input) (*policy.Decision, error) {
	p.calls.Add(1)
	return p.dec, nil
}

func (p *countingPolicy) Explain(_ context.Context, _ policy.Input) (*apitypes.ExplainResult, error) {
	return &apitypes.ExplainResult{}, nil
}

func (p *countingPolicy) SnapshotInfo() (int64, string, []string, bool) {
	return 1, "digest-1", nil, false
}

func TestExecute_CacheHitSkipsEvaluateAndRewrite(t *testing.T) {
	pol := &countingPolicy{dec: &policy.Decision{Outcome: policy.OutcomeAllow}}
	rw := &countingRewriter{}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}, rows: [][]any{{int64(1)}}}
	cache := policycache.New(16, time.Minute)

	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   pol,
		Rewriter: rw,
		Executor: ex,
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		Cache:    cache,
	})

	run := func() {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id FROM t"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		for res.Rows.Next() {
			var v any
			_ = res.Rows.Scan(&v)
		}
		_ = res.Rows.Close()
	}

	run()
	run()

	if got := pol.calls.Load(); got != 1 {
		t.Errorf("Evaluate called %d times, want 1 (second served from cache)", got)
	}
	if got := rw.calls.Load(); got != 1 {
		t.Errorf("Rewrite called %d times, want 1 (second served from cache)", got)
	}
	// Both runs still execute (cache only memoises decision+rewrite).
	if ex.execCount != 2 {
		t.Errorf("Executor ran %d times, want 2", ex.execCount)
	}
}

func TestExecute_CacheMissOnDifferentLiteral(t *testing.T) {
	pol := &countingPolicy{dec: &policy.Decision{Outcome: policy.OutcomeAllow}}
	cache := policycache.New(16, time.Minute)
	svc := queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   pol,
		Rewriter: &countingRewriter{},
		Executor: &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		Cache:    cache,
	})
	for _, sql := range []string{"SELECT * FROM t WHERE id = 1", "SELECT * FROM t WHERE id = 2"} {
		res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		_ = res.Rows.Close()
	}
	if got := pol.calls.Load(); got != 2 {
		t.Errorf("Evaluate called %d times, want 2 (different SQL literals must not share a cache entry)", got)
	}
}
