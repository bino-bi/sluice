// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd_PrintsIdentity(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"version"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	s := out.String()
	if !strings.HasPrefix(s, "sluice ") {
		t.Fatalf("version output should start with \"sluice \", got: %q", s)
	}
	if !strings.Contains(s, "parser=") {
		t.Fatalf("version output should include parser=, got: %q", s)
	}
}
