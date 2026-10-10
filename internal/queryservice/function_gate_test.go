// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/policycache"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

func TestExecute_FunctionGate(t *testing.T) {
	cases := []struct {
		name   string
		shape  parser.QueryShape
		reject bool
	}{
		{"no functions", parser.QueryShape{}, false},
		{"allowlisted functions", parser.QueryShape{Functions: []parser.FuncRef{
			{Name: "upper"}, {Name: "generate_series", InFrom: true},
		}}, false},
		{"table function", parser.QueryShape{Functions: []parser.FuncRef{{Name: "query_table", InFrom: true}}}, true},
		{"expression function", parser.QueryShape{Functions: []parser.FuncRef{{Name: "current_setting"}}}, true},
		{"unsupported FROM item", parser.QueryShape{UnsupportedFrom: []string{"TABLESAMPLE"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAudit{}
			ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
			pol := &countingPolicy{dec: &policy.Decision{Outcome: policy.OutcomeAllow}}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{ast: &fakeAST{shape: tc.shape, stmt: parser.StmtSelect, fingerprint: "fp-in"}},
				Policy:   pol,
				Rewriter: &fakeRewriter{},
				Executor: ex,
				Audit:    a,
				Clock:    func() time.Time { return time.Unix(1713600000, 0) },
			})
			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
				SQL:    "SELECT 1",
				Origin: queryservice.OriginREST,
			})
			if !tc.reject {
				if err != nil {
					t.Fatalf("execute: %v", err)
				}
				_ = res.Rows.Close()
				return
			}
			if err == nil {
				t.Fatal("expected function gate reject")
			}
			ae := pkgerr.FromError(err)
			if ae.Code != pkgerr.CodeACLRejected {
				t.Fatalf("code = %s, want %s", ae.Code, pkgerr.CodeACLRejected)
			}
			if ae.QueryID == "" {
				t.Fatal("reject must carry the query id")
			}
			if got := pol.calls.Load(); got != 0 {
				t.Fatalf("policy evaluated %d times, the gate must run first", got)
			}
			if ex.execCount != 0 {
				t.Fatalf("executor must not run on reject, got %d calls", ex.execCount)
			}
			recs := a.all()
			if len(recs) != 1 {
				t.Fatalf("reject must emit exactly 1 audit record, got %d", len(recs))
			}
			if recs[0].Decision != audit.DecisionReject {
				t.Fatalf("decision = %s, want reject", recs[0].Decision)
			}
			if recs[0].Message != ae.Message || recs[0].Message == "" {
				t.Fatalf("audit message = %q, client message = %q", recs[0].Message, ae.Message)
			}
		})
	}
}

// A memoised allow for the same SQL text and identity must not let a
// request past the gate: the gate runs before the cache lookup.
func TestExecute_FunctionGate_RunsBeforeCache(t *testing.T) {
	pol := &countingPolicy{dec: &policy.Decision{Outcome: policy.OutcomeAllow}}
	ex := &fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}}
	fp := &fakeParser{ast: &fakeAST{stmt: parser.StmtSelect, fingerprint: "fp-in"}}
	svc := queryservice.New(queryservice.Options{
		Parser:   fp,
		Policy:   pol,
		Rewriter: &countingRewriter{},
		Executor: ex,
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		Cache:    policycache.New(16, time.Minute),
	})
	const sql = "SELECT id FROM t"

	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if err != nil {
		t.Fatalf("seed execute: %v", err)
	}
	_ = res.Rows.Close()
	if pol.calls.Load() != 1 || ex.execCount != 1 {
		t.Fatalf("seed run: policy=%d exec=%d, want 1 and 1", pol.calls.Load(), ex.execCount)
	}

	// Same SQL text, so the cache holds an allow for it.
	fp.ast.shape = parser.QueryShape{Functions: []parser.FuncRef{{Name: "query_table", InFrom: true}}}
	_, err = svc.Execute(context.Background(), queryservice.QueryRequest{SQL: sql})
	if ae := pkgerr.FromError(err); err == nil || ae.Code != pkgerr.CodeACLRejected {
		t.Fatalf("err = %v, want ACL_REJECTED despite the cached allow", err)
	}
	if ex.execCount != 1 {
		t.Fatalf("executor ran %d times, want 1 (the cached allow must not be served)", ex.execCount)
	}
}
