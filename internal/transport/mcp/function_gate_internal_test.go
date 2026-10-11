// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
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
