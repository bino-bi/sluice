// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/policy"
)

// TestBuildRuntime_ReloadDisabled_ManualReloadStillWired locks in the
// contract that policies.reload only gates fsnotify watching: with
// reload: false the watcher, the reload subscriber, and the admin
// reloader all still exist, and a synchronous Reload (SIGHUP /
// POST /admin/reload path) applies a policy change without Start.
func TestBuildRuntime_ReloadDisabled_ManualReloadStillWired(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
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

	// The policy and schema metrics are registered with the runtime, so
	// GET /metrics shows them. Before, nothing registered them.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	registered := map[string]bool{}
	for _, f := range families {
		registered[f.GetName()] = true
	}
	for _, name := range []string{"sluice_policy_eval_duration_seconds", "sluice_schema_refresh_duration_seconds"} {
		if !registered[name] {
			t.Errorf("metric %s is not registered", name)
		}
	}

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

// A Rego module that does not compile fails the reload as an invalid
// policy. Nothing is published, and every engine keeps the policies it
// had: the YAML member must not move on while the OPA member stays behind.
func TestBuildRuntime_BrokenRegoFailsReload(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	moduleDir := filepath.Join(dir, "rego")
	for _, d := range []string{policyDir, moduleDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	module := filepath.Join(moduleDir, "main.rego")
	const goodModule = "package sluice\nimport rego.v1\nmain := {\"abstain\": true}\n"
	if err := os.WriteFile(module, []byte(goodModule), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  reload: false
  engine: composite
  composite:
    members: [yaml, opa]
  opa:
    moduleDir: ` + moduleDir + `
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
	published := deps.registry.Current().Version
	yamlVersion := deps.policyEng.Snapshot().Version

	// A new YAML policy and a broken module arrive together.
	const policyYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analytics, priority: 100 }
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: ["analytics"] }
        resources: { catalogs: ["warehouse"] }
`
	if err := os.WriteFile(filepath.Join(policyDir, "allow.yaml"), []byte(policyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(module, []byte("package sluice\nmain := {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deps.watcher.Reload(ctx); !errors.Is(err, policy.ErrSnapshotInvalid) {
		t.Fatalf("reload with a broken module: err = %v, want ErrSnapshotInvalid", err)
	}
	if got := deps.registry.Current().Version; got != published {
		t.Errorf("published version = %d, want it to stay %d", got, published)
	}
	if got := deps.policyEng.Snapshot().Version; got != yamlVersion {
		t.Errorf("YAML engine moved to version %d, want it to stay %d", got, yamlVersion)
	}

	// With the module repaired the same reload goes through.
	if err := os.WriteFile(module, []byte(goodModule), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deps.watcher.Reload(ctx); err != nil {
		t.Fatalf("reload with the repaired module: %v", err)
	}
	// The engine carries the published version: the policy cache keys on
	// it and treats version 0 as not cacheable.
	if got, want := deps.policyEng.Snapshot().Version, deps.registry.Current().Version; got != want || got == yamlVersion {
		t.Errorf("YAML engine version = %d, want the published version %d", got, want)
	}
}
