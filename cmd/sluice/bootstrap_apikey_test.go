// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/secrets"
)

// admin.token is the token itself, or a secret reference that is resolved
// at start. An empty secret must fail: an empty token turns the check of
// the admin API off.
func TestResolveAdminToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_TOKEN_SET", "from-env")
	t.Setenv("ADMIN_TOKEN_EMPTY", "")
	r := secrets.NewResolver(secrets.ResolverOptions{})
	for name, tc := range map[string]struct {
		token, want string
		fail        bool
	}{
		"plain text":                 {token: "s3cret", want: "s3cret"},
		"empty":                      {token: "", want: ""},
		"from the environment":       {token: "secret://env/ADMIN_TOKEN_SET", want: "from-env"},
		"from a file, trimmed":       {token: "secret://file/" + file, want: "from-file"},
		"empty secret":               {token: "secret://env/ADMIN_TOKEN_EMPTY", fail: true},
		"scheme in another case":     {token: "Secret://env/ADMIN_TOKEN_SET", want: "from-env"},
		"space before the reference": {token: " secret://env/ADMIN_TOKEN_SET", want: "from-env"},
		"missing variable":           {token: "secret://env/ADMIN_TOKEN_MISSING", fail: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolveAdminToken(context.Background(), r, tc.token)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("resolveAdminToken(%q) = %q, %v; want %q, error %v", tc.token, got, err, tc.want, tc.fail)
			}
		})
	}
}

// A secret file often ends with a newline. The pepper and the genesis are
// trimmed like every other secret.
func TestPepperAndGenesisAreTrimmed(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"pepper": "pep\n", "genesis": "gen \r\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := secrets.NewResolver(secrets.ResolverOptions{})

	seed, err := resolveGenesisSeed(context.Background(), r, &config.FileSinkConfig{Genesis: "secret://file/" + filepath.Join(dir, "genesis")})
	if err != nil || string(seed) != "gen" {
		t.Errorf("genesis seed = %q, %v; want %q", seed, err, "gen")
	}
	pepper, err := resolvePepper(context.Background(), "secret://file/"+filepath.Join(dir, "pepper"))
	if err != nil || string(pepper) != "pep" {
		t.Errorf("pepper from a reference = %q, %v; want %q", pepper, err, "pep")
	}
	// A pepper of nothing but a line end would be an empty pepper.
	if err := os.WriteFile(filepath.Join(dir, "blank"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePepper(context.Background(), "secret://file/"+filepath.Join(dir, "blank")); err == nil {
		t.Error("an empty pepper secret gave no error")
	}
	// A pepper typed on the command line stays as typed.
	if raw, err := resolvePepper(context.Background(), "pep\n"); err != nil || string(raw) != "pep\n" {
		t.Errorf("raw pepper = %q, %v; want it unchanged", raw, err)
	}
}
