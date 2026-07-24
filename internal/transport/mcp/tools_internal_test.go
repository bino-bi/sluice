// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
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
