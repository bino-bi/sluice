// SPDX-License-Identifier: AGPL-3.0-or-later

package duckdbfile

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
		Name: "x", Type: Type,
		Raw: map[string]any{"path": "/tmp/nope-this-does-not-exist-99999.duckdb"},
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
	path := filepath.Join(dir, "warehouse.duckdb")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "warehouse", Type: Type, Raw: map[string]any{"path": path},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	if ds.Name() != "warehouse" {
		t.Errorf("Name = %q; want warehouse", ds.Name())
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

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("duckdbfile driver did not self-register via init()")
	}
}
