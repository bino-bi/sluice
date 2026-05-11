// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

// TestAPIKeyHashCommandMatchesLibrary confirms the CLI output is
// byte-identical to identity.ComputeHash — the request path and the
// operator-facing rotation tool must agree, otherwise keys computed
// with the CLI will 401 at runtime.
func TestAPIKeyHashCommandMatchesLibrary(t *testing.T) {
	cmd := newAPIKeyCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"hash", "--pepper", "pep", "--id", "hello", "--material", "world"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	want := hex.EncodeToString(identity.ComputeHash([]byte("pep"), "hello", "world"))
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestAPIKeyHashCommandRequiresFlags rejects missing input instead of
// silently hashing empty strings, which would produce a valid-looking
// but useless digest.
func TestAPIKeyHashCommandRequiresFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing pepper", []string{"hash", "--id", "x", "--material", "y"}},
		{"missing id", []string{"hash", "--pepper", "p", "--material", "y"}},
		{"missing material", []string{"hash", "--pepper", "p", "--id", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newAPIKeyCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatal("want error; got nil")
			}
		})
	}
}
