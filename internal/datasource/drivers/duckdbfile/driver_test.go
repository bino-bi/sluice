// SPDX-License-Identifier: AGPL-3.0-or-later

package duckdbfile

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

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

func TestAttachSchemaHealthCheck(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shop.duckdb")
	src, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.ExecContext(ctx, `CREATE TABLE customers (id INTEGER NOT NULL, email VARCHAR)`); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ds, err := newDriver(ctx, pkgds.Spec{Name: "shop", Type: Type, Raw: map[string]any{"path": path}})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	if err := ds.HealthCheck(ctx, conn, pkgds.HealthOptions{}); err == nil {
		t.Error("HealthCheck passed before Attach")
	}
	for range 2 { // a second ATTACH is not an error
		if err := ds.Attach(ctx, conn, pkgds.AttachOptions{}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if err := ds.HealthCheck(ctx, conn, pkgds.HealthOptions{}); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO shop.main.customers VALUES (1, 'a')`); err == nil {
		t.Error("INSERT succeeded on a read-only attach")
	}

	sch, err := ds.Schema(ctx, conn)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if len(sch.Schemas) != 1 || len(sch.Schemas[0].Tables) != 1 {
		t.Fatalf("Schema = %+v; want one schema with one table", sch)
	}
	cols := sch.Schemas[0].Tables[0].Columns
	if len(cols) != 2 || cols[0].Name != "id" || cols[0].Nullable || cols[1].Name != "email" || !cols[1].Nullable {
		t.Errorf("columns = %+v", cols)
	}
}
