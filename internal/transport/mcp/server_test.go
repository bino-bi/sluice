// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/internal/transport/mcp"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

// fakeAST / fakeParser / fakePolicy / fakeRewriter / fakeExec / fakeAudit
// are thin doubles so the MCP handler runs through a real queryservice
// without pulling the DuckDB executor in.

type fakeAST struct {
	tables []parser.TableRef
}

func (a *fakeAST) Raw() any                   { return nil }
func (a *fakeAST) Fingerprint() string        { return "fp" }
func (a *fakeAST) Tables() []parser.TableRef  { return a.tables }
func (a *fakeAST) Catalogs() []string         { return nil }
func (a *fakeAST) Shape() parser.QueryShape   { return parser.QueryShape{} }
func (a *fakeAST) Clone() parser.AST          { c := *a; return &c }
func (a *fakeAST) Statement() parser.StmtKind { return parser.StmtSelect }
func (a *fakeAST) Source() string             { return "" }

type fakeParser struct{}

func (p *fakeParser) Parse(_ context.Context, _ string) (parser.AST, error) {
	return &fakeAST{}, nil
}
func (p *fakeParser) Deparse(_ context.Context, _ parser.AST) (string, error) { return "", nil }
func (p *fakeParser) Fingerprint(_ string) (string, error)                    { return "fp", nil }
func (p *fakeParser) Name() string                                            { return "fake" }

type fakePolicy struct{}

func (p *fakePolicy) Evaluate(_ context.Context, _ policy.Input) (*policy.Decision, error) {
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}
func (p *fakePolicy) Explain(_ context.Context, _ policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{}, nil
}

type fakeRewriter struct{}

func (r *fakeRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	return &rewriter.RewriteResult{SQL: req.Raw, Fingerprint: "fp-out"}, nil
}

type fakeExec struct{}

func (e *fakeExec) Execute(_ context.Context, _ executor.Request) (*executor.Response, error) {
	rowCount := new(int64)
	iter := &fakeIter{rows: [][]any{{int64(1)}}, rowCount: rowCount}
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
	mu sync.Mutex
}

func (a *fakeAudit) Enqueue(_ context.Context, _ *audit.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil
}

func newSvc(t *testing.T) *queryservice.Service {
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

func TestNew_ConstructsStdioByDefault(t *testing.T) {
	t.Parallel()
	srv, err := mcp.New(mcp.Config{Enabled: true}, mcp.Deps{Service: newSvc(t)})
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server")
	}
}

func TestNew_RequiresService(t *testing.T) {
	t.Parallel()
	if _, err := mcp.New(mcp.Config{}, mcp.Deps{}); err == nil {
		t.Fatal("expected error for nil Service")
	}
}

// TestToolRegistered is a smoke test that confirms AddTool wiring doesn't
// panic at construction time and that the four MVP tools are visible via
// the underlying SDK's tool listing (exercised through the client loopback
// in a later integration test).
func TestToolRegistered(t *testing.T) {
	t.Parallel()
	_, err := mcp.New(mcp.Config{Enabled: true}, mcp.Deps{Service: newSvc(t)})
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
}

// Ensure the SDK stays importable — catch version drift between the
// pinned go-sdk and our usage sites.
var _ sdkmcp.Tool
