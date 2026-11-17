// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parserbackend"
)

// stageTestConfig writes a server config with an audit directory and the
// extra text, and returns its path and an empty policy directory.
func stageTestConfig(t *testing.T, auditDir, extra string) (cfgPath, policyDir string) {
	t.Helper()
	dir := t.TempDir()
	policyDir = filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "sluice.yaml")
	cfg := "policies:\n  reload: false\naudit:\n  file:\n    path: " + auditDir + "\n" + extra
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, policyDir
}

func closeDeps(t *testing.T, deps *runtimeDeps) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deps.Close(ctx)
	})
}

// A failure in the last stage closes what the earlier stages built. The
// audit file sink of the core stage holds the lock of the audit
// directory, so a second start on the same directory works only when the
// failed start released it.
func TestBuildRuntime_FailedStartClosesEarlierStages(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	auditDir := filepath.Join(t.TempDir(), "audit")
	// An admin API without a token on every interface fails in the
	// transport stage, after core, identity and service.
	bad, policyDir := stageTestConfig(t, auditDir, "admin:\n  enabled: true\n  listen: 0.0.0.0:0\n")
	if _, err := buildRuntime(context.Background(), bad, policyDir); err == nil || !strings.Contains(err.Error(), "admin.token") {
		t.Fatalf("start with an open admin API: %v, want the admin.token refusal", err)
	}
	good, policyDir := stageTestConfig(t, auditDir, "")
	deps, err := buildRuntime(context.Background(), good, policyDir)
	if err != nil {
		t.Fatalf("start after a failed start: %v, want the audit directory released", err)
	}
	closeDeps(t, deps)
}

// `sluice mcp` builds only what it runs: no REST or admin server, no
// approval broker. Settings of those parts do not stop it.
func TestBuildRuntimeFor_MCPBuildsOnlyWhatItRuns(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	cfg, policyDir := stageTestConfig(t, filepath.Join(t.TempDir(), "audit"), `
admin:
  enabled: true
  listen: 0.0.0.0:0
approval:
  publicBaseUrl: https://sluice.example
`)
	deps, err := buildRuntimeFor(context.Background(), runMCP, cfg, policyDir)
	if err != nil {
		t.Fatalf("buildRuntimeFor(runMCP): %v", err)
	}
	closeDeps(t, deps)
	if deps.rest != nil || deps.admin != nil || deps.approvals != nil || deps.mcp != nil || deps.synth != nil {
		t.Errorf("mcp runtime built rest=%v admin=%v approvals=%v mcp=%v synth=%v, want none",
			deps.rest != nil, deps.admin != nil, deps.approvals != nil, deps.mcp != nil, deps.synth != nil)
	}
	if deps.service == nil || deps.watcher == nil || deps.identifier == nil {
		t.Error("mcp runtime misses the query service, the watcher or the identifier")
	}
	if !deps.refuseApprovals {
		t.Error("mcp runtime accepts a reload with an ApprovalPolicy")
	}
}
