// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	ok := []string{"pg", "my_cat", "_x", "A$b", "cat_42"}
	bad := []string{"", "1cat", "my-cat", "cat;drop", `"quoted"`, "foo bar"}
	for _, s := range ok {
		if err := ValidateIdentifier(s); err != nil {
			t.Errorf("ValidateIdentifier(%q) = %v; want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateIdentifier(s); err == nil {
			t.Errorf("ValidateIdentifier(%q) = nil; want error", s)
		}
	}
}

func TestEscapeSQLString(t *testing.T) {
	cases := map[string]string{
		"hello":          "hello",
		"it's tricky":    "it''s tricky",
		"'leading quote": "''leading quote",
		"multi''quotes":  "multi''''quotes",
	}
	for in, want := range cases {
		if got := EscapeSQLString(in); got != want {
			t.Errorf("EscapeSQLString(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestIsAlreadyAttached(t *testing.T) {
	if !IsAlreadyAttached(errors.New("catalog my_cat is already attached")) {
		t.Error("should match 'already attached'")
	}
	if !IsAlreadyAttached(errors.New("relation already exists")) {
		t.Error("should match 'already exists'")
	}
	if IsAlreadyAttached(errors.New("something else")) {
		t.Error("should not match unrelated error")
	}
	if IsAlreadyAttached(nil) {
		t.Error("nil error must not match")
	}
}

func TestBuildCreateSecret(t *testing.T) {
	stmt, err := BuildCreateSecret("pg_main", "postgres", []SecretArg{
		{Key: "host", Value: "db.example"},
		{Key: "password", Value: "abc'123"},
		{Key: "user", Value: "svc"},
	})
	if err != nil {
		t.Fatalf("BuildCreateSecret: %v", err)
	}
	wantSubstrs := []string{
		"CREATE OR REPLACE SECRET pg_main",
		"TYPE POSTGRES",
		"HOST 'db.example'",
		"PASSWORD 'abc''123'",
		"USER 'svc'",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(stmt, s) {
			t.Errorf("stmt missing substring %q; got:\n%s", s, stmt)
		}
	}

	// Order-independence: same args in different order produce the same stmt.
	stmt2, err := BuildCreateSecret("pg_main", "postgres", []SecretArg{
		{Key: "user", Value: "svc"},
		{Key: "password", Value: "abc'123"},
		{Key: "host", Value: "db.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stmt != stmt2 {
		t.Errorf("BuildCreateSecret is not order-independent:\n%s\n---\n%s", stmt, stmt2)
	}
}

func TestBuildCreateSecretRejectsBadIdentifiers(t *testing.T) {
	if _, err := BuildCreateSecret("bad name", "postgres", nil); err == nil {
		t.Error("expected error for invalid secret name")
	}
	if _, err := BuildCreateSecret("ok", "", nil); err == nil {
		t.Error("expected error for empty secret type")
	}
	if _, err := BuildCreateSecret("ok", "postgres", []SecretArg{{Key: "bad-key", Value: "v"}}); err == nil {
		t.Error("expected error for invalid arg key")
	}
}
