// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/parserbackend"
)

// An admin API without a token starts only on loopback or with the
// explicit opt-in.
func TestCheckAdminAnonymous(t *testing.T) {
	cases := []struct {
		listen, token string
		allowAnon     bool
		ok            bool
	}{
		{":9091", "", false, false},
		{"0.0.0.0:9091", "", false, false},
		{"10.0.0.5:9091", "", false, false},
		{":9091", "tok", false, true},
		{":9091", "", true, true},
		{"127.0.0.1:9091", "", false, true},
		{"127.1.2.3:9091", "", false, true},
		{"[::1]:9091", "", false, true},
		{"localhost:9091", "", false, true},
	}
	for _, tc := range cases {
		err := checkAdminAnonymous(config.AdminConfig{Listen: tc.listen, AllowAnonymous: tc.allowAnon}, tc.token)
		if (err == nil) != tc.ok {
			t.Errorf("listen %q token %q allowAnonymous %v: err = %v, want ok = %v", tc.listen, tc.token, tc.allowAnon, err, tc.ok)
		}
	}
}

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
