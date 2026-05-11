// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import "testing"

func TestParse_Env(t *testing.T) {
	u, err := Parse("secret://env/PG_PASSWORD")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Provider != "env" {
		t.Errorf("Provider = %q, want %q", u.Provider, "env")
	}
	if u.Name() != "PG_PASSWORD" {
		t.Errorf("Name() = %q, want %q", u.Name(), "PG_PASSWORD")
	}
	if u.Raw != "secret://env/PG_PASSWORD" {
		t.Errorf("Raw = %q", u.Raw)
	}
}

func TestParse_FileAbsolute(t *testing.T) {
	u, err := Parse("secret://file//var/run/secrets/pii")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Provider != "file" {
		t.Errorf("Provider = %q", u.Provider)
	}
	if u.Path != "//var/run/secrets/pii" {
		t.Errorf("Path = %q (expected leading slashes preserved)", u.Path)
	}
}

func TestParse_VaultFragment(t *testing.T) {
	u, err := Parse("secret://vault/secret/data/pii#value")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if u.Fragment != "value" {
		t.Errorf("Fragment = %q, want %q", u.Fragment, "value")
	}
	if u.Provider != "vault" {
		t.Errorf("Provider = %q", u.Provider)
	}
}

func TestParse_Errors(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"env://PG_PASSWORD", // missing secret:// wrapper
		"secret:///nothing", // empty provider
		"://bad",            // missing scheme
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}
