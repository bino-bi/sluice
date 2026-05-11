// SPDX-License-Identifier: AGPL-3.0-or-later

package motherduck

import (
	"context"
	"testing"

	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

func TestNewDriverRequiresDatabase(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "md", Type: Type, Raw: map[string]any{"tokenRef": "secret://env/MD_TOKEN"},
	})
	if err == nil {
		t.Fatal("expected error when database is missing")
	}
}

func TestNewDriverRequiresTokenRef(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "md", Type: Type, Raw: map[string]any{"database": "sample_data"},
	})
	if err == nil {
		t.Fatal("expected error when tokenRef is missing")
	}
}

func TestNewDriverTrimsMdPrefix(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "md", Type: Type,
		Raw: map[string]any{
			"database": "md:sample_data",
			"tokenRef": "secret://env/MD_TOKEN",
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	d := ds.(*driver)
	if d.database != "sample_data" {
		t.Errorf("database = %q; want sample_data", d.database)
	}
}

func TestNewDriverRejectsEmptyAfterPrefix(t *testing.T) {
	_, err := newDriver(context.Background(), pkgds.Spec{
		Name: "md", Type: Type,
		Raw: map[string]any{"database": "md:", "tokenRef": "secret://env/MD_TOKEN"},
	})
	if err == nil {
		t.Fatal("expected error when database is 'md:' only")
	}
}

func TestNewDriverSuccess(t *testing.T) {
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: "md", Type: Type,
		Raw: map[string]any{
			"database": "sample_data",
			"tokenRef": "secret://env/MD_TOKEN",
		},
	})
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	if ds.Name() != "md" {
		t.Errorf("Name = %q; want md", ds.Name())
	}
	if ds.Type() != Type {
		t.Errorf("Type = %q; want %q", ds.Type(), Type)
	}
	if !ds.Readonly() {
		t.Error("Readonly = false; MVP is read-only")
	}
	if err := ds.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("motherduck driver did not self-register via init()")
	}
}
