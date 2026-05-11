// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
	"github.com/bino-bi/sluice/pkg/datasource/testfakes"
)

// The datasource package assumes drivers register via init(); for
// isolation these tests use a unique type per test and register a
// testfakes-backed factory. pkg/datasource's registry panics on
// duplicate registration, so we pick a prefix unlikely to clash with
// real drivers.
const fakeType = "fake_registry_test"

func init() {
	pkgds.Register(fakeType, func(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
		if spec.Name == "boom" {
			return nil, errors.New("synthetic failure")
		}
		return testfakes.New(spec.Name, pkgds.Schema{Catalog: spec.Name}), nil
	})
}

func dsSpec(name string) *apitypes.DataSource {
	return &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.dev/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec:     apitypes.DataSourceSpec{Type: apitypes.DataSourceType(fakeType)},
	}
}

func TestRegistryInstantiatesEveryDataSource(t *testing.T) {
	snap := &datasource.Snapshot{DataSources: []*apitypes.DataSource{dsSpec("pg"), dsSpec("mysql")}}
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       snap,
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	cats := r.Catalogs()
	if len(cats) != 2 {
		t.Fatalf("Catalogs = %v; want 2", cats)
	}
	for _, want := range []string{"pg", "mysql"} {
		if _, err := r.Lookup(want); err != nil {
			t.Errorf("Lookup(%q): %v", want, err)
		}
	}
}

func TestRegistryUnknownTypeFailsFast(t *testing.T) {
	ds := &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.dev/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: "x"},
		Spec:     apitypes.DataSourceSpec{Type: "nosuch"},
	}
	_, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds}},
		HealthInterval: -1,
		FailFast:       true,
	})
	if err == nil {
		t.Fatal("expected error for unknown type with FailFast")
	}
}

func TestRegistryDegradedModeSkipsFailing(t *testing.T) {
	snap := &datasource.Snapshot{
		DataSources: []*apitypes.DataSource{dsSpec("ok"), dsSpec("boom")},
	}
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       snap,
		HealthInterval: -1,
		FailFast:       false,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	if cats := r.Catalogs(); len(cats) != 1 || cats[0] != "ok" {
		t.Fatalf("Catalogs = %v; want [ok]", cats)
	}
}

func TestRegistryLookupUnknown(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	if _, err := r.Lookup("ghost"); !errors.Is(err, datasource.ErrUnknownCatalog) {
		t.Fatalf("Lookup err = %v; want ErrUnknownCatalog", err)
	}
}

func TestRegistryStatusesPreservesInsertionOrder(t *testing.T) {
	snap := &datasource.Snapshot{DataSources: []*apitypes.DataSource{
		dsSpec("gamma"),
		dsSpec("alpha"),
		dsSpec("beta"),
	}}
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       snap,
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	statuses := r.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("Statuses = %d; want 3", len(statuses))
	}
	wantOrder := []string{"gamma", "alpha", "beta"}
	for i, s := range statuses {
		if s.Name != wantOrder[i] {
			t.Errorf("Statuses[%d].Name = %q; want %q", i, s.Name, wantOrder[i])
		}
	}
}

func TestRegistryCloseIsIdempotent(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{dsSpec("one")}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestRegistryRequiresSnapshot(t *testing.T) {
	_, err := datasource.New(context.Background(), datasource.Options{HealthInterval: -1})
	if err == nil {
		t.Fatal("expected error when Snapshot is nil")
	}
}

func TestRegistryMarkSchemaPulled(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{dsSpec("pg")}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	before := r.Statuses()[0].SchemaPulledAt
	if !before.IsZero() {
		t.Errorf("expected zero SchemaPulledAt; got %v", before)
	}
	// Use a frozen "now"; time.Time{}.IsZero is always true, so any
	// non-zero time will do.
	r.MarkSchemaPulled("pg", nowish())
	if s := r.Statuses()[0]; s.SchemaPulledAt.IsZero() {
		t.Error("SchemaPulledAt still zero after MarkSchemaPulled")
	}
}

// nowish is a tiny wrapper so the test reads naturally.
func nowish() time.Time { return time.Now() }
