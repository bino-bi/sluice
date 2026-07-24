// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuildRuntime_ReloadDisabled_ManualReloadStillWired locks in the
// contract that policies.reload only gates fsnotify watching: with
// reload: false the watcher, the reload subscriber, and the admin
// reloader all still exist, and a synchronous Reload (SIGHUP /
// POST /admin/reload path) applies a policy change without Start.
func TestBuildRuntime_ReloadDisabled_ManualReloadStillWired(t *testing.T) {
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatalf("mkdir policies: %v", err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  dir: ` + policyDir + `
  reload: false
audit:
  file:
    path: ` + filepath.Join(dir, "audit") + `
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx := context.Background()
	deps, err := buildRuntime(ctx, cfgPath, policyDir)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deps.Close(shutdownCtx)
	}()

	if deps.watcher == nil {
		t.Fatal("watcher must be constructed with policies.reload: false")
	}
	if reloaderFromWatcher(deps.watcher) == nil {
		t.Fatal("admin reloader must be wired with policies.reload: false")
	}

	before := deps.registry.Current().Digest

	policyYAML := `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analytics
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["warehouse"]
`
	if err := os.WriteFile(filepath.Join(policyDir, "allow.yaml"), []byte(policyYAML), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	// No Start() was called: this is the manual (SIGHUP / admin) path.
	if err := deps.watcher.Reload(ctx); err != nil {
		t.Fatalf("manual reload: %v", err)
	}
	after := deps.registry.Current().Digest
	if after == before {
		t.Fatalf("snapshot digest unchanged after manual reload (before=%q)", before)
	}
}
