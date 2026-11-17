// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

// Every HTTP request is authenticated, so HTTP without an identifier is a
// wiring error, not an open listener.
func TestNew_HTTPNeedsIdentifier(t *testing.T) {
	_, err := New(Config{Transport: TransportStreamableHTTP, HTTPListen: "127.0.0.1:0"}, Deps{Service: testService()})
	if err == nil || !strings.Contains(err.Error(), "Identifier") {
		t.Fatalf("err = %v, want an error that names the Identifier", err)
	}
}

// A body over the cap is not buffered whole.
func TestStreamable_BodyCap(t *testing.T) {
	_, url := newStreamableTestServer(t, Config{})
	big := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"x":"` + strings.Repeat("a", maxBodyBytes) + `"}}`
	status, _, body := mcpPost(t, url, "alice", "", big)
	if status < 400 || status == http.StatusUnauthorized {
		t.Fatalf("status = %d, want a refusal of the body: %s", status, body)
	}
}

type panicIdentifier struct{}

func (panicIdentifier) Identify(context.Context, *http.Request) (*identity.UserCtx, error) {
	panic("boom")
}
func (panicIdentifier) Name() string { return "panic" }

// A panic gives 500 ERR_INTERNAL, not a reset connection.
func TestStreamable_PanicRecovered(t *testing.T) {
	_, url := newStreamableTestServerWith(t, Config{}, func(d *Deps) { d.Identifier = panicIdentifier{} })
	status, _, body := mcpPost(t, url, "alice", "", rpcInitialize)
	if status != http.StatusInternalServerError || !strings.Contains(body, `"code":"ERR_INTERNAL"`) {
		t.Fatalf("status %d body %s, want 500 ERR_INTERNAL", status, body)
	}
}
