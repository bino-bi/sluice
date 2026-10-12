// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

func TestDecodePage_RoundTrip(t *testing.T) {
	t.Parallel()
	cur := encodeCursor("shop.main.orders")
	if cur == "" || cur == "shop.main.orders" {
		t.Fatalf("cursor not opaque: %q", cur)
	}
	page, err := decodePage(25, cur)
	if err != nil {
		t.Fatalf("decodePage: %v", err)
	}
	if page.Limit != 25 || page.After != "shop.main.orders" {
		t.Fatalf("page = %+v", page)
	}
	if encodeCursor("") != "" {
		t.Fatal("empty cursor must stay empty")
	}
}

func TestDecodePage_InvalidCursor(t *testing.T) {
	t.Parallel()
	_, err := decodePage(0, "%%%not-base64%%%")
	if err == nil {
		t.Fatal("expected error for malformed cursor")
	}
	if !strings.Contains(err.Error(), "invalid cursor") {
		t.Fatalf("err = %v", err)
	}
}

func TestToolListTables_InvalidCursorIsToolError(t *testing.T) {
	t.Parallel()
	// The cursor is rejected before any service call, so a bare Server
	// with no deps suffices.
	s := &Server{}
	res, _, err := s.toolListTables(context.Background(), &sdkmcp.CallToolRequest{},
		ListTablesArgs{Catalog: "shop", Cursor: "!!"})
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
	if !strings.Contains(text, "ERR_SYNTAX") {
		t.Fatalf("tool error text missing ERR_SYNTAX: %q", text)
	}
}

type scanFailRows struct{ served bool }

func (r *scanFailRows) Next() bool {
	first := !r.served
	r.served = true
	return first
}
func (*scanFailRows) Scan(...any) error { return errors.New("scan: boom") }
func (*scanFailRows) Err() error        { return nil }
func (*scanFailRows) Close() error      { return nil }

type scanFailExecutor struct{}

func (scanFailExecutor) Execute(context.Context, executor.Request) (*executor.Response, error) {
	return &executor.Response{
		Columns:  []executor.ColumnInfo{{Name: "id"}},
		Rows:     &scanFailRows{},
		RowCount: new(int64),
	}, nil
}

// A row error after Execute has no query_id of its own. The tool result
// and the log line of the ERR_INTERNAL must still name the query, and the
// cause must stay out of the tool result.
func TestToolExecuteSQL_RowErrorIsLoggedWithQueryID(t *testing.T) {
	t.Parallel()
	var log bytes.Buffer
	s := &Server{
		lg: slog.New(slog.NewJSONHandler(&log, nil)),
		deps: Deps{Service: queryservice.New(queryservice.Options{
			Parser:   pgquery.New(parser.Options{}),
			Policy:   allowAllPolicy{},
			Rewriter: passRewriter{},
			Executor: scanFailExecutor{},
			Audit:    discardAudit{},
		})},
	}
	res, out, err := s.toolExecuteSQL(context.Background(), &sdkmcp.CallToolRequest{}, ExecuteSQLArgs{SQL: "SELECT 1 AS id"})
	if err != nil || res == nil || !res.IsError || out.QueryID == "" {
		t.Fatalf("res = %+v, query_id = %q, err = %v, want a tool error with a query_id", res, out.QueryID, err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "ERR_INTERNAL") || !strings.Contains(text, "query_id="+out.QueryID) || strings.Contains(text, "boom") {
		t.Fatalf("tool error text = %q, want the code and query_id without the cause", text)
	}
	got := log.String()
	for _, want := range []string{`"level":"ERROR"`, `"query_id":"` + out.QueryID + `"`, "scan: boom"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %q, want it to contain %q", got, want)
		}
	}

	log.Reset()
	s.toolErrorResult(context.Background(), pkgerr.New(pkgerr.CodeACLDenied))
	if log.Len() != 0 {
		t.Fatalf("log = %q, want nothing for a client error", log.String())
	}
}
