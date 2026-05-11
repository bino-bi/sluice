// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

// Package integration runs against real backends selected by the
// `-tags=integration` build tag. The single smoke test here exists so
// `make test-integration` has something to execute before the real
// driver-backed tests (Slice 7) get their containers wired up.
package integration

import (
	"os/exec"
	"strings"
	"testing"
)

// TestBinaryVersionSmoke asserts `sluice version` runs from a built
// binary under the integration lane. It is the cheapest possible
// integration check — if this fails, nothing else in the lane will
// pass either.
func TestBinaryVersionSmoke(t *testing.T) {
	bin, err := exec.LookPath("sluice")
	if err != nil {
		// Fall back to the repo-local binary so the test works both
		// in CI (where sluice is installed on PATH) and from a fresh
		// checkout (where it was just built by `make build`).
		bin = "../../bin/sluice"
	}
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("sluice version failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "sluice") {
		t.Fatalf("unexpected version output: %q", out)
	}
}
