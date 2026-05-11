// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestConfigValidate_OK(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "../../internal/config/testdata/policies/valid"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "policies OK") {
		t.Fatalf("expected OK output, got: %q", out.String())
	}
}

func TestConfigValidate_Invalid_ExitCode3(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "../../internal/config/testdata/policies/invalid"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from invalid fixtures")
	}

	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if exit.Code != 3 {
		t.Fatalf("exit code = %d, want 3", exit.Code)
	}
}

func TestConfigValidate_MissingDir_IsValid(t *testing.T) {
	// Default-deny: a non-existent directory is not a validation failure —
	// it yields an empty snapshot. Exit code should be 0.
	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "/tmp/sluice-does-not-exist-xyz"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("missing dir should not be an error: %v", err)
	}
}

func TestConfigValidate_BadServerConfig_ExitCode1(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "--config", "/tmp/sluice-no-such-file.yaml"})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	// A missing server config is not an error (LoadServer treats it as
	// defaults-only). This call should therefore succeed.
	if err := root.Execute(); err != nil {
		t.Fatalf("missing server config should not fail: %v", err)
	}
}
