// SPDX-License-Identifier: Apache-2.0

package testfakes_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/pkg/datasource"
	"github.com/bino-bi/sluice/pkg/datasource/testfakes"
)

func TestFakeBasics(t *testing.T) {
	t.Parallel()
	schema := datasource.Schema{
		Catalog: "fake",
		Schemas: []datasource.SchemaNS{{
			Name: "public",
			Tables: []datasource.Table{{
				Name: "orders",
				Columns: []datasource.Column{
					{Name: "id", SQLType: "INT"},
					{Name: "total", SQLType: "DOUBLE"},
				},
			}},
		}},
	}
	ds := testfakes.New("fake", schema, testfakes.WithType("unit-test"))
	if ds.Name() != "fake" {
		t.Errorf("Name = %q, want fake", ds.Name())
	}
	if ds.Type() != "unit-test" {
		t.Errorf("Type = %q, want unit-test", ds.Type())
	}
	if !ds.Readonly() {
		t.Error("Readonly should default to true")
	}

	got, err := ds.Schema(context.Background(), nil)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if got.Catalog != "fake" || len(got.Schemas) != 1 {
		t.Errorf("Schema unexpected: %+v", got)
	}

	if err := ds.HealthCheck(context.Background(), nil, datasource.HealthOptions{}); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}

	if err := ds.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ds.HealthCheck(context.Background(), nil, datasource.HealthOptions{}); !errors.Is(err, datasource.ErrClosed) {
		t.Errorf("HealthCheck after Close = %v, want ErrClosed", err)
	}
}

func TestFakeHooks(t *testing.T) {
	t.Parallel()
	var attachCalled, healthCalled bool
	ds := testfakes.New("with-hooks", datasource.Schema{},
		testfakes.WithAttachHook(func(_ context.Context, _ *sql.Conn, _ datasource.AttachOptions) error {
			attachCalled = true
			return nil
		}),
		testfakes.WithHealthHook(func(_ context.Context) error {
			healthCalled = true
			return nil
		}),
	)
	if err := ds.Attach(context.Background(), nil, datasource.AttachOptions{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !attachCalled {
		t.Error("AttachHook not invoked")
	}
	if err := ds.HealthCheck(context.Background(), nil, datasource.HealthOptions{}); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !healthCalled {
		t.Error("HealthHook not invoked")
	}
}

func TestFakeReadOnlyOption(t *testing.T) {
	t.Parallel()
	ds := testfakes.New("rw", datasource.Schema{}, testfakes.WithReadOnly(false))
	if ds.Readonly() {
		t.Error("Readonly should be false")
	}
}
