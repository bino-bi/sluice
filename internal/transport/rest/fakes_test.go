// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

// fakeAST implements parser.AST. We only care about Fingerprint and Tables.
type fakeAST struct {
	shape       parser.QueryShape
	tables      []parser.TableRef
	fingerprint string
	stmt        parser.StmtKind
	source      string
}

func (a *fakeAST) Raw() any                   { return nil }
func (a *fakeAST) Fingerprint() string        { return a.fingerprint }
func (a *fakeAST) Tables() []parser.TableRef  { return a.tables }
func (a *fakeAST) Catalogs() []string         { return nil }
func (a *fakeAST) Shape() parser.QueryShape   { return a.shape }
func (a *fakeAST) Clone() parser.AST          { c := *a; return &c }
func (a *fakeAST) Statement() parser.StmtKind { return a.stmt }
func (a *fakeAST) Source() string             { return a.source }

type fakeParser struct{}

func (p *fakeParser) Parse(_ context.Context, sql string) (parser.AST, error) {
	return &fakeAST{stmt: parser.StmtSelect, fingerprint: "fp-in", source: sql}, nil
}
func (p *fakeParser) Deparse(_ context.Context, _ parser.AST) (string, error) { return "", nil }
func (p *fakeParser) Fingerprint(_ string) (string, error)                    { return "fp-in", nil }
func (p *fakeParser) Name() string                                            { return "fake" }

type fakePolicy struct{}

func (p *fakePolicy) Evaluate(_ context.Context, _ policy.Input) (*policy.Decision, error) {
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}
func (p *fakePolicy) Explain(_ context.Context, _ policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{Effective: pkgapi.EffectiveDecision{Decision: "allow"}}, nil
}

type fakeRewriter struct{}

func (r *fakeRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	return &rewriter.RewriteResult{SQL: req.Raw, Fingerprint: "fp-out"}, nil
}

// fakeExec echoes a canned result.
type fakeExec struct{}

func (e *fakeExec) Execute(_ context.Context, req executor.Request) (*executor.Response, error) {
	sql := req.SQL
	if sql == "" {
		return nil, errors.New("fake exec: empty sql")
	}
	rowCount := new(int64)
	if contains(sql, " AS name") {
		iter := &fakeIter{
			rows:     [][]any{{int64(1), "hi"}},
			rowCount: rowCount,
		}
		resp := &executor.Response{
			Columns:  []executor.ColumnInfo{{Name: "id"}, {Name: "name"}},
			Rows:     iter,
			RowCount: rowCount,
		}
		iter.parent = resp
		return resp, nil
	}
	// Default: single-column "n" with value 1.
	iter := &fakeIter{
		rows:     [][]any{{int64(1)}},
		rowCount: rowCount,
	}
	resp := &executor.Response{
		Columns:  []executor.ColumnInfo{{Name: "n"}},
		Rows:     iter,
		RowCount: rowCount,
	}
	iter.parent = resp
	return resp, nil
}

type fakeIter struct {
	rows     [][]any
	rowCount *int64
	idx      int
	closed   bool
	parent   *executor.Response
}

func (it *fakeIter) Next() bool {
	if it.closed || it.idx >= len(it.rows) {
		return false
	}
	*it.rowCount++
	return true
}
func (it *fakeIter) Scan(dest ...any) error {
	row := it.rows[it.idx]
	if len(dest) != len(row) {
		return errors.New("fake: scan mismatch")
	}
	for i, v := range row {
		if d, ok := dest[i].(*any); ok {
			*d = v
		}
	}
	it.idx++
	return nil
}
func (it *fakeIter) Err() error { return nil }
func (it *fakeIter) Close() error {
	if it.closed {
		return nil
	}
	it.closed = true
	return nil
}

type fakeAudit struct {
	mu      sync.Mutex
	records []*audit.Record
}

func (a *fakeAudit) Enqueue(_ context.Context, r *audit.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, r)
	return nil
}

func newQuerySvc(t *testing.T) *queryservice.Service {
	t.Helper()
	return queryservice.New(queryservice.Options{
		Parser:   &fakeParser{},
		Policy:   &fakePolicy{},
		Rewriter: &fakeRewriter{},
		Executor: &fakeExec{},
		Audit:    &fakeAudit{},
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})
}

func contains(s, sub string) bool {
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
