// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlitefile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestNewDriverRequiresPath(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{Name: "x", Type: Type, Raw: map[string]any{}})
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
}

func TestNewDriverRequiresExistingFile(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "x",
		Type: Type,
		Raw:  map[string]any{"path": "/tmp/nope-this-file-does-not-exist-12345.sqlite"},
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err not os.ErrNotExist: %v", err)
	}
}

func TestNewDriverSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smoke.sqlite")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "mydata",
		Type: Type,
		Raw:  map[string]any{"path": path},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	if ds.Name() != "mydata" {
		t.Errorf("Name = %q; want mydata", ds.Name())
	}
	if ds.Type() != Type {
		t.Errorf("Type = %q; want %q", ds.Type(), Type)
	}
	if !ds.Readonly() {
		t.Error("Readonly = false; want true (MVP is read-only)")
	}
	if err := ds.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestValidateIdentifier(t *testing.T) {
	ok := []string{"pg", "my_cat", "_x", "A$b", "cat_42"}
	bad := []string{"", "1cat", "my-cat", "cat;drop", `"quoted"`, "foo bar"}
	for _, s := range ok {
		if err := validateIdentifier(s); err != nil {
			t.Errorf("validateIdentifier(%q) = %v; want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := validateIdentifier(s); err == nil {
			t.Errorf("validateIdentifier(%q) = nil; want error", s)
		}
	}
}

func TestEscapeSQLString(t *testing.T) {
	cases := map[string]string{
		`hello`:          `hello`,
		`it's tricky`:    `it''s tricky`,
		`'leading quote`: `''leading quote`,
	}
	for in, want := range cases {
		if got := escapeSQLString(in); got != want {
			t.Errorf("escapeSQLString(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestIsAlreadyAttached(t *testing.T) {
	if !isAlreadyAttached(errors.New("catalog my_cat is already attached")) {
		t.Error("should match 'already attached'")
	}
	if !isAlreadyAttached(errors.New("relation already exists")) {
		t.Error("should match 'already exists'")
	}
	if isAlreadyAttached(errors.New("something else")) {
		t.Error("should not match unrelated error")
	}
	if isAlreadyAttached(nil) {
		t.Error("nil error must not match")
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	_, ok := pkgds.Lookup(Type)
	if !ok {
		t.Fatal("sqlitefile driver did not self-register via init()")
	}
}
