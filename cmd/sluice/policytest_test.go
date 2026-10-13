// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A policy that decodes but does not compile is a validation failure:
// exit status 3, as the help says, not the status 1 of an I/O error.
func TestPolicyTest_CompileError_ExitCode3(t *testing.T) {
	dir := t.TempDir()
	// The mask needs a secret reference in saltRef.
	const policy = `
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  mask:
    type: hash
    args: { algorithm: hmac_sha256, saltRef: "plain text" }
`
	if err := os.WriteFile(filepath.Join(dir, "mask.yaml"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tests"), 0o700); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"policy", "test", dir})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()

	var exit *exitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want exit status 3; output %q", err, out.String())
	}
}
