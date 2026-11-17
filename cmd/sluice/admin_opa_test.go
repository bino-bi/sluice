// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/parserbackend"
)

// With engine opa the YAML engine gets no snapshot. The admin API reads
// version and digest from the published config, so it still shows them
// and a reload moves config_version.
func TestAdmin_OPAEngineShowsConfigVersion(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	moduleDir := filepath.Join(dir, "rego")
	for _, d := range []string{policyDir, moduleDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.rego"), []byte("package sluice\nimport rego.v1\nmain := {\"abstain\": true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "sluice.yaml")
	cfg := `
policies:
  reload: false
  engine: opa
  opa:
    moduleDir: ` + moduleDir + `
admin:
  enabled: true
  listen: 127.0.0.1:0
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		deps.Close(ctx)
	}()

	call := func(method, path string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		deps.admin.Handler().ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d: %s", method, path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	h := call(http.MethodGet, "/admin/healthz")
	if h["config_version"] != float64(1) || h["config_digest"] == nil {
		t.Fatalf("healthz = %v, want config_version 1 and a digest", h)
	}
	if r := call(http.MethodPost, "/admin/reload"); r["digest"] == nil {
		t.Fatalf("reload = %v, want a digest", r)
	}
	if h := call(http.MethodGet, "/admin/healthz"); h["config_version"] != float64(2) {
		t.Fatalf("healthz after reload = %v, want config_version 2", h)
	}
}
