// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/queryservice"
)

// sampleRowsExecutor returns two rows of one column and keeps the request.
type sampleRowsExecutor struct{ last executor.Request }

func (e *sampleRowsExecutor) Execute(_ context.Context, req executor.Request) (*executor.Response, error) {
	e.last = req
	n := new(int64)
	return &executor.Response{
		Columns:  []executor.ColumnInfo{{Name: "id"}},
		Rows:     &sampleRows{rows: []int64{1, 2}, n: n},
		RowCount: n,
	}, nil
}

type sampleRows struct {
	rows []int64
	i    int
	n    *int64
}

func (r *sampleRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	*r.n++
	return true
}

func (r *sampleRows) Scan(dest ...any) error {
	*(dest[0].(*any)) = r.rows[r.i-1]
	return nil
}

func (r *sampleRows) Err() error   { return nil }
func (r *sampleRows) Close() error { return nil }

func toolText(res *sdkmcp.CallToolResult) string {
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func TestToolSampleTable_ReturnsRows(t *testing.T) {
	t.Parallel()
	ex := &sampleRowsExecutor{}
	s := &Server{lg: slog.New(slog.DiscardHandler), deps: Deps{Service: queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   allowAllPolicy{},
		Rewriter: passRewriter{},
		Executor: ex,
		Audit:    discardAudit{},
	})}}
	seed := int64(7)
	res, out, err := s.toolSampleTable(context.Background(), &sdkmcp.CallToolRequest{}, SampleTableArgs{
		Table: "shop.main.customers", Rows: 2, Seed: &seed,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}
	if out.QueryID == "" || len(out.Rows) != 2 || out.RowCount != 2 || out.Truncated {
		t.Errorf("output = %+v", out)
	}
	if want := `SELECT * FROM "shop"."main"."customers"`; ex.last.SQL != want {
		t.Errorf("SQL = %q, want %q", ex.last.SQL, want)
	}
	if ex.last.MaxRows != 3 {
		t.Errorf("MaxRows = %d, want 3", ex.last.MaxRows)
	}
}

func TestToolSampleTable_ValidationError(t *testing.T) {
	t.Parallel()
	s := &Server{lg: slog.New(slog.DiscardHandler), deps: Deps{Service: queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   allowAllPolicy{},
		Rewriter: passRewriter{},
		Executor: unreachableExecutor{},
		Audit:    discardAudit{},
	})}}
	res, _, err := s.toolSampleTable(context.Background(), &sdkmcp.CallToolRequest{}, SampleTableArgs{
		Table: "shop.main.customers", Rate: 2,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res == nil || !res.IsError || !strings.Contains(toolText(res), "ERR_SYNTAX") {
		t.Fatalf("result = %+v, want an ERR_SYNTAX error", res)
	}
}

func TestToolSampleTable_FillsRequestFacts(t *testing.T) {
	t.Parallel()
	pol := &capturePolicy{}
	s := &Server{lg: slog.New(slog.DiscardHandler), deps: Deps{Service: queryservice.New(queryservice.Options{
		Parser:   pgquery.New(parser.Options{}),
		Policy:   pol,
		Rewriter: passRewriter{},
		Executor: unreachableExecutor{},
		Audit:    discardAudit{},
	})}}
	ctx := context.WithValue(context.Background(), remoteIPKey{}, "10.1.2.3")
	req := &sdkmcp.CallToolRequest{Extra: &sdkmcp.RequestExtra{Header: http.Header{
		"User-Agent": {"agent/1"},
		"X-Region":   {"eu"},
	}}}
	if _, _, err := s.toolSampleTable(ctx, req, SampleTableArgs{Table: "a.b.c", Rows: 1}); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	f := pol.in.Request
	if f == nil {
		t.Fatal("the policy got no request facts")
	}
	if f.RemoteIP.String() != "10.1.2.3" || f.UserAgent != "agent/1" || f.Headers["x-region"] != "eu" {
		t.Errorf("facts = %+v", f)
	}
}
