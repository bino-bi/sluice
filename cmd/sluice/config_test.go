// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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

func TestConfigValidate_UnenforceableServerConfig_ExitCode3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sluice.yaml")
	if err := os.WriteFile(path, []byte("datasources:\n  reload: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "--config", path})

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unenforceable config")
	}
	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if exit.Code != 3 {
		t.Fatalf("exit code = %d, want 3", exit.Code)
	}
	if !strings.Contains(errOut.String(), "datasources.reload") {
		t.Fatalf("stderr must name the field, got: %q", errOut.String())
	}
}

func TestConfigValidate_MCPStdioWithoutCredential_ExitCode3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sluice.yaml")
	if err := os.WriteFile(path, []byte("mcp:\n  enabled: true\n  transport: stdio\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "--config", path})

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for stdio without credential")
	}
	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if exit.Code != 3 {
		t.Fatalf("exit code = %d, want 3", exit.Code)
	}
	if !strings.Contains(errOut.String(), "mcp.tokenRef") {
		t.Fatalf("stderr must name the fix, got: %q", errOut.String())
	}
}

func TestConfigValidate_ApprovalPoliciesWithoutBaseURL_ExitCode3(t *testing.T) {
	dir := t.TempDir()
	policy := `apiVersion: sluice.bino.bi/v1alpha1
kind: ApprovalPolicy
metadata: { name: approve-x, priority: 100 }
spec:
  match:
    any:
      - resources: { tables: [people] }
  reason: "needs sign-off"
`
	if err := os.WriteFile(filepath.Join(dir, "approval.yaml"), []byte(policy), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "sluice.yaml")
	if err := os.WriteFile(cfgPath, []byte("logging:\n  level: info\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "--config", cfgPath, dir})

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for ApprovalPolicies without publicBaseUrl")
	}
	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("want *exitError, got %T: %v", err, err)
	}
	if exit.Code != 3 {
		t.Fatalf("exit code = %d, want 3", exit.Code)
	}
	if !strings.Contains(errOut.String(), "approval.publicBaseUrl") {
		t.Fatalf("stderr must name the field, got: %q", errOut.String())
	}
}

func TestConfigValidate_MissingServerConfig_ExitCode1(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"config", "validate", "--config", filepath.Join(t.TempDir(), "no-such-file.yaml")})

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	// A path that is set must exist, as in `sluice serve`.
	err := root.Execute()
	var exit *exitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("err = %v, want exit status 1", err)
	}
}

// A key the server config does not know is refused, also without
// --strict, as `sluice serve` does.
func TestConfigValidate_ChecksServerConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte("rest:\n  listen: \":8080\"\npolicies:\n  dir: ./policies.d\nlimitz:\n  maxRows: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		root := newRootCmd()
		root.SetArgs(append([]string{"config", "validate"}, args...))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		err := root.Execute()
		return out.String(), err
	}

	for _, args := range [][]string{{"--config", path}, {"--config", path, "--strict"}} {
		out, err := run(args...)
		var exit *exitError
		if !errors.As(err, &exit) || exit.Code != 3 {
			t.Fatalf("%v: err = %v, want exit status 3; output %q", args, err, out)
		}
		for _, key := range []string{"dir", "limitz"} {
			if !strings.Contains(out, key) {
				t.Errorf("output %q does not name the unknown key %q", out, key)
			}
		}
	}

	good := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(good, []byte("rest:\n  listen: \":8080\"\n  tls:\n    certFile: a\n    keyFile: b\napproval:\n  webhooks:\n    - url: https://example.test/hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run("--config", good, "--strict"); err != nil {
		t.Fatalf("known keys with --strict: %v, output %q", err, out)
	}
}
