// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err == nil {
		t.Fatal("sluice mcp started with an ApprovalPolicy loaded")
	}
	for _, want := range []string{"ApprovalPolicy", "sluice serve", "mcp.enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
