// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/queryservice"
)

// keySubjectIdentifier takes the X-Api-Key value as the subject, so one
// test can act as several callers.
type keySubjectIdentifier struct{}

func (keySubjectIdentifier) Identify(_ context.Context, r *http.Request) (*identity.UserCtx, error) {
	k := r.Header.Get("X-Api-Key")
	if k == "" {
		return nil, identity.ErrNoCredential
	}
	return &identity.UserCtx{Subject: k, Issuer: "test", AuthMethod: identity.AuthMethodAPIKey}, nil
}
func (keySubjectIdentifier) Name() string { return "key-subject" }

// mcpPost sends one JSON-RPC message over Streamable HTTP.
func mcpPost(t *testing.T, url, key, session, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

const (
	rpcInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	rpcInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rpcWhoAmI      = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`
)

func newStreamableTestServer(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()
	cfg.Transport = TransportStreamableHTTP
	s, err := New(cfg, Deps{
		Service: queryservice.New(queryservice.Options{
			Parser:   pgquery.New(parser.Options{}),
			Policy:   allowAllPolicy{},
			Rewriter: passRewriter{},
			Executor: unreachableExecutor{},
			Audit:    discardAudit{},
		}),
		Identifier: keySubjectIdentifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.httpHandler())
	t.Cleanup(ts.Close)
	return s, ts.URL
}

func openSession(t *testing.T, url, key string) string {
	t.Helper()
	status, hdr, body := mcpPost(t, url, key, "", rpcInitialize)
	session := hdr.Get("Mcp-Session-Id")
	if status != http.StatusOK || session == "" {
		t.Fatalf("initialize: status %d, session %q, body %s", status, session, body)
	}
	if status, _, body := mcpPost(t, url, key, session, rpcInitialized); status >= 300 {
		t.Fatalf("initialized: status %d, body %s", status, body)
	}
	return session
}

func TestStreamable_ToolRunsAsCallerOfTheRequest(t *testing.T) {
	_, url := newStreamableTestServer(t, Config{})
	session := openSession(t, url, "alice")

	status, _, body := mcpPost(t, url, "alice", session, rpcWhoAmI)
	if status != http.StatusOK || !strings.Contains(body, "subject=alice") {
		t.Fatalf("alice in her session: status %d, body %s", status, body)
	}

	// A session id of another caller must not carry that caller's subject.
	status, _, body = mcpPost(t, url, "bob", session, rpcWhoAmI)
	if status != http.StatusOK || !strings.Contains(body, "subject=bob") {
		t.Fatalf("bob with alice's session id: status %d, body %s", status, body)
	}

	if status, _, _ := mcpPost(t, url, "", session, rpcWhoAmI); status != http.StatusUnauthorized {
		t.Fatalf("no credential with a session id: status %d, want 401", status)
	}
}

func TestStreamable_AnonymousDoesNotInheritTheSession(t *testing.T) {
	_, url := newStreamableTestServer(t, Config{AllowAnonymous: true})
	session := openSession(t, url, "alice")

	status, _, body := mcpPost(t, url, "", session, rpcWhoAmI)
	if status != http.StatusOK || !strings.Contains(body, `"anonymous":true`) {
		t.Fatalf("no credential with alice's session id: status %d, body %s", status, body)
	}
}

// The docs state this status.
func TestStreamable_GetEventStreamIs405(t *testing.T) {
	_, url := newStreamableTestServer(t, Config{})
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Api-Key", "alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}
}

func TestStreamable_ClosedRequestCancelsTheCall(t *testing.T) {
	s, url := newStreamableTestServer(t, Config{})
	started := make(chan struct{})
	cancelled := make(chan struct{})
	// Runs before the server closes, so a call that is never cancelled
	// cannot keep the server from closing.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	sdkmcp.AddTool(s.mcp, &sdkmcp.Tool{Name: "block"},
		func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ WhoAmIArgs) (*sdkmcp.CallToolResult, WhoAmIOutput, error) {
			close(started)
			select {
			case <-ctx.Done():
				close(cancelled)
			case <-release:
			}
			return &sdkmcp.CallToolResult{}, WhoAmIOutput{}, nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"block","arguments":{}}}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Api-Key", "alice")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("tool context not cancelled after the client closed the request")
	}
}
