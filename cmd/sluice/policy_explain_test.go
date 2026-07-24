// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgapi "github.com/bino-bi/sluice/pkg/apitypes"
)

const explainAllowPolicyYAML = `
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

func writeExplainPolicyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "allow.yaml"), []byte(explainAllowPolicyYAML), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return dir
}

func TestPolicyExplain_SQLHappyPath(t *testing.T) {
	dir := writeExplainPolicyDir(t)
	root := newRootCmd()
	root.SetArgs([]string{"policy", "explain",
		"--policies-dir", dir,
		"--user", "alice",
		"--groups", "analytics",
		"--sql", "SELECT id FROM warehouse.public.orders",
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "decision: allow") {
		t.Fatalf("expected allow decision, got: %q", got)
	}
	if !strings.Contains(got, "allow-analytics") {
		t.Fatalf("expected matching policy name in output, got: %q", got)
	}
}

func TestPolicyExplain_SQLParseErrorExit1(t *testing.T) {
	dir := writeExplainPolicyDir(t)
	root := newRootCmd()
	root.SetArgs([]string{"policy", "explain",
		"--policies-dir", dir,
		"--user", "alice",
		"--sql", "SELECT FROM FROM WHERE",
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected parse error")
	}
	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if exit.Code != 1 {
		t.Fatalf("exit code = %d, want 1", exit.Code)
	}
}

func TestPolicyExplain_SQLAndTableCombine(t *testing.T) {
	dir := writeExplainPolicyDir(t)
	root := newRootCmd()
	root.SetArgs([]string{"policy", "explain",
		"--policies-dir", dir,
		"--user", "alice",
		"--groups", "analytics",
		"--sql", "SELECT id FROM warehouse.public.orders",
		"--table", "other.public.events",
		"--json",
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v (out=%s)", err, out.String())
	}
	var result pkgapi.ExplainResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode ExplainResult: %v (out=%s)", err, out.String())
	}
	// Both the parsed table and the explicit --table must be part of the
	// explained resource set ("first.table (+ N more)" rendering).
	res := result.Resource
	if !strings.Contains(res, "warehouse.public.orders") || !strings.Contains(res, "+ 1 more") {
		t.Fatalf("resource %q missing parsed or explicit table", res)
	}
}
