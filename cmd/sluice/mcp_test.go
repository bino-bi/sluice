// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/secrets"
)

// headerCheckingIdentifier resolves any request carrying the expected
// bearer token, proving the resolved secret actually reaches the identity
// pipeline.
type headerCheckingIdentifier struct {
	wantBearer string
}

func (h *headerCheckingIdentifier) Identify(_ context.Context, r *http.Request) (*identity.UserCtx, error) {
	if got := r.Header.Get("Authorization"); got == "Bearer "+h.wantBearer {
		return &identity.UserCtx{Subject: "agent", Issuer: "test"}, nil
	}
	return nil, identity.ErrNoCredential
}

func (h *headerCheckingIdentifier) Name() string { return "header-check" }

func TestResolveMCPPinnedUser(t *testing.T) {
	t.Setenv("SLUICE_TEST_MCP_TOKEN", "tok-123\n")
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})
	id := &headerCheckingIdentifier{wantBearer: "tok-123"}

	t.Run("tokenRef resolves and pins", func(t *testing.T) {
		scfg := config.DefaultServerConfig()
		scfg.MCP.Enabled = true
		scfg.MCP.TokenRef = "secret://env/SLUICE_TEST_MCP_TOKEN"
		u, err := resolveMCPPinnedUser(context.Background(), &scfg, resolver, id, discardLogger())
		if err != nil {
			t.Fatalf("resolveMCPPinnedUser: %v", err)
		}
		if u == nil || u.Subject != "agent" {
			t.Fatalf("pinned user = %+v, want subject agent", u)
		}
	})

	t.Run("allowAnonymous runs unpinned", func(t *testing.T) {
		scfg := config.DefaultServerConfig()
		scfg.MCP.Enabled = true
		scfg.MCP.AllowAnonymous = true
		u, err := resolveMCPPinnedUser(context.Background(), &scfg, resolver, id, discardLogger())
		if err != nil {
			t.Fatalf("resolveMCPPinnedUser: %v", err)
		}
		if u != nil {
			t.Fatalf("pinned user = %+v, want nil (anonymous)", u)
		}
	})

	t.Run("no credential refuses", func(t *testing.T) {
		scfg := config.DefaultServerConfig()
		scfg.MCP.Enabled = true
		_, err := resolveMCPPinnedUser(context.Background(), &scfg, resolver, id, discardLogger())
		if err == nil {
			t.Fatal("expected refusal without credential or allowAnonymous")
		}
		if !strings.Contains(err.Error(), "mcp.tokenRef") {
			t.Fatalf("error must name the config keys, got: %v", err)
		}
	})

	t.Run("streamable_http needs no pin", func(t *testing.T) {
		scfg := config.DefaultServerConfig()
		scfg.MCP.Enabled = true
		scfg.MCP.Transport = "streamable_http"
		u, err := resolveMCPPinnedUser(context.Background(), &scfg, resolver, id, discardLogger())
		if err != nil || u != nil {
			t.Fatalf("streamable_http: got (%+v, %v), want (nil, nil)", u, err)
		}
	})

	t.Run("bad credential fails startup", func(t *testing.T) {
		t.Setenv("SLUICE_TEST_MCP_BAD", "wrong-token")
		scfg := config.DefaultServerConfig()
		scfg.MCP.Enabled = true
		scfg.MCP.TokenRef = "secret://env/SLUICE_TEST_MCP_BAD"
		_, err := resolveMCPPinnedUser(context.Background(), &scfg, resolver, id, discardLogger())
		if err == nil {
			t.Fatal("expected authentication failure for a wrong token")
		}
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// An approver decides over the REST API. `sluice mcp` does not start it,
// so a held query could never be released: the command refuses to start.
func TestMCPCmd_RefusesApprovalPolicies(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	const policyYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ApprovalPolicy
metadata: { name: approve-pii-reads, priority: 100 }
spec:
  match:
    any:
      - resources: { tables: [people] }
  when:
    columnsAccessed: ["ssn"]
  reason: "needs a data steward"
`
	if err := os.WriteFile(filepath.Join(policyDir, "approval.yaml"), []byte(policyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  reload: false
approval:
  publicBaseUrl: https://sluice.example
audit:
  file:
    path: ` + filepath.Join(dir, "audit") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--policies-dir", policyDir, "--allow-anonymous"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.ExecuteContext(context.Background())
	var exit *exitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("err = %v, want exit status 1 with an ApprovalPolicy loaded", err)
	}
	for _, want := range []string{"ApprovalPolicy", "sluice serve", "mcp.enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// REST and MCP over HTTP draw from the same buckets ahead of the login, so
// a client cannot double its budget by using both listeners.
func TestBuildRuntime_MCPSharesTheRateLimitOfREST(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  reload: false
mcp:
  enabled: true
  transport: streamable_http
  listen: 127.0.0.1:0
  allowAnonymous: true
limits:
  globalRps: 0.001
  globalBurst: 1
audit:
  file:
    path: ` + filepath.Join(dir, "audit") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	deps, err := buildRuntime(context.Background(), cfgPath, policyDir)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deps.Close(shutdownCtx)
	}()

	// REST takes the only token of the bucket.
	w := httptest.NewRecorder()
	deps.rest.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/query", strings.NewReader(`{"sql":"SELECT 1"}`)))
	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("first request was rate limited: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	deps.mcp.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("MCP status = %d, want 429 from the bucket that REST emptied: %s", w.Code, w.Body.String())
	}
}

// runMCPCmd runs `sluice mcp` with an empty policy directory and returns
// its error.
func runMCPCmd(t *testing.T, args ...string) error {
	t.Helper()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := "policies:\n  reload: false\naudit:\n  file:\n    path: " + filepath.Join(dir, "audit") + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newMCPCmd()
	cmd.SetArgs(append([]string{"--config", cfgPath, "--policies-dir", policyDir}, args...))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.ExecuteContext(context.Background())
}

// A short name of the HTTP transport is the HTTP transport: the command
// must not ask for the credential that only stdio pins.
func TestMCPCmd_TransportAliasNeedsNoStdioCredential(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	for _, name := range []string{"http", "streamable-http", "streamablehttp", "streamable_http"} {
		// The config has no mcp.listen, so the command stops there.
		err := runMCPCmd(t, "--transport", name)
		if err == nil || !strings.Contains(err.Error(), "mcp.listen") {
			t.Errorf("--transport %s: err = %v, want the missing mcp.listen", name, err)
		}
	}
}

func TestMCPCmd_UnknownTransport(t *testing.T) {
	err := runMCPCmd(t, "--transport", "sse")
	var exit *exitError
	if !errors.As(err, &exit) || exit.Code != 1 || !strings.Contains(err.Error(), `"sse"`) {
		t.Fatalf("err = %v, want exit status 1 that names the transport", err)
	}
}
