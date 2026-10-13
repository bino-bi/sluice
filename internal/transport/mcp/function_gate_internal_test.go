// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

type allowAllPolicy struct{}

func (allowAllPolicy) Evaluate(context.Context, policy.Input) (*policy.Decision, error) {
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

func (allowAllPolicy) Explain(context.Context, policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{}, nil
}

type passRewriter struct{}

func (passRewriter) Rewrite(_ context.Context, req rewriter.RewriteRequest) (*rewriter.RewriteResult, error) {
	return &rewriter.RewriteResult{SQL: req.Raw}, nil
}

type unreachableExecutor struct{}

func (unreachableExecutor) Execute(context.Context, executor.Request) (*executor.Response, error) {
	return nil, errors.New("executor must not be reached")
}

type discardAudit struct{}

func (discardAudit) Enqueue(context.Context, *audit.Record) error { return nil }

// The function gate lives in the query service. This locks that the MCP
// execute_sql tool reaches it: real parser, policy that allows everything.
func TestToolExecuteSQL_FunctionGateRejects(t *testing.T) {
	t.Parallel()
	s := &Server{deps: Deps{Service: queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   allowAllPolicy{},
		Rewriter: passRewriter{},
		Executor: unreachableExecutor{},
		Audit:    discardAudit{},
	})}}
	res, _, err := s.toolExecuteSQL(context.Background(), &sdkmcp.CallToolRequest{}, ExecuteSQLArgs{
		SQL: "SELECT t.id FROM (SELECT 1 FROM shop.main.customers LIMIT 1) x, query_table('shop.main.customers') t",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError result, got %+v", res)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "ACL_REJECTED") || !strings.Contains(text, "query_table") {
		t.Fatalf("tool error text = %q, want ACL_REJECTED naming query_table", text)
	}
}

// capturePolicy keeps the input of the last evaluation.
type capturePolicy struct{ in policy.Input }

func (p *capturePolicy) Evaluate(_ context.Context, in policy.Input) (*policy.Decision, error) {
	p.in = in
	return &policy.Decision{Outcome: policy.OutcomeAllow}, nil
}

func (p *capturePolicy) Explain(context.Context, policy.Input) (*pkgapi.ExplainResult, error) {
	return &pkgapi.ExplainResult{}, nil
}

// Over HTTP the policy sees the headers of the tool call as request.*, and
// the address of its subject. Stdio has no request, so it sees none.
func TestToolExecuteSQL_FillsRequestFacts(t *testing.T) {
	t.Parallel()
	pol := &capturePolicy{}
	// The executor fails after the policy ran. Only the policy input counts.
	s := &Server{lg: slog.New(slog.DiscardHandler), deps: Deps{Service: queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   pol,
		Rewriter: passRewriter{},
		Executor: unreachableExecutor{},
		Audit:    discardAudit{},
	})}}
	ctx := identity.WithUser(context.Background(), &identity.UserCtx{Subject: "agent", RemoteAddr: "10.1.2.3"})
	req := &sdkmcp.CallToolRequest{Extra: &sdkmcp.RequestExtra{Header: http.Header{
		"User-Agent": {"agent/1"},
		"X-Region":   {"eu"},
		"X-Api-Key":  {"k.secret"},
	}}}
	if _, _, err := s.toolExecuteSQL(ctx, req, ExecuteSQLArgs{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	f := pol.in.Request
	if f == nil {
		t.Fatal("the policy got no request facts")
	}
	if f.RemoteIP.String() != "10.1.2.3" || f.UserAgent != "agent/1" || f.Headers["x-region"] != "eu" {
		t.Errorf("facts = %+v", f)
	}
	if _, ok := f.Headers["x-api-key"]; ok {
		t.Error("the API key reached the policy")
	}

	if _, _, err := s.toolExecuteSQL(ctx, &sdkmcp.CallToolRequest{}, ExecuteSQLArgs{SQL: "SELECT 1"}); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if pol.in.Request != nil {
		t.Errorf("a call without an HTTP request got facts: %+v", pol.in.Request)
	}
}
