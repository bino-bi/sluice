// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/parserbackend"
)

// A typo in a key or in the --config path must stop the start, not run
// with the defaults. Both checks run before anything is built.
func TestBuildRuntime_RefusesUnknownKeyAndMissingFile(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}

	typo := filepath.Join(dir, "typo.yaml")
	if err := os.WriteFile(typo, []byte("rest:\n  tsl:\n    certFile: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := buildRuntime(context.Background(), typo, policyDir)
	if err == nil || !strings.Contains(err.Error(), "tsl") {
		t.Fatalf("unknown key: err = %v, want an error that names tsl", err)
	}

	_, err = buildRuntime(context.Background(), filepath.Join(dir, "missing.yaml"), policyDir)
	if err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("missing file: err = %v, want an error that names the file", err)
	}
}
