// SPDX-License-Identifier: AGPL-3.0-or-later

package synthetic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/synth/build"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

const (
	catalog = "syn"
	build1  = "20261102T101500Z-a1b2c3"
	build2  = "20261102T111500Z-d4e5f6"
)

func testSpec(ratio float64) *apitypes.SyntheticSpec {
	return &apitypes.SyntheticSpec{
		Source: "crm",
		Seed:   "secret://env/SEED",
		Tables: []apitypes.SyntheticTable{{
			Name: "crm.customers",
			Size: apitypes.SyntheticSize{Ratio: &ratio},
		}},
	}
}

func specHash(t *testing.T, spec *apitypes.SyntheticSpec) string {
	t.Helper()
	h, err := build.SpecHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// writeBuild writes a snapshot with crm.customers of rows rows and its
// report, as a build would.
func writeBuild(t *testing.T, l build.Layout, id, hash string, rows int) {
	t.Helper()
	for _, d := range []string{l.SnapshotsDir(), l.ReportsDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("duckdb", l.Snapshot(id))
	if err != nil {
		t.Fatal(err)
	}
	q := fmt.Sprintf(`CREATE SCHEMA crm; CREATE TABLE crm.customers AS SELECT range AS id FROM range(%d)`, rows)
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	rep, err := json.Marshal(build.Report{BuildID: id, SpecHash: hash, Result: build.ResultSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Report(id), rep, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setCurrent(t *testing.T, l build.Layout, id string) {
	t.Helper()
	if err := os.MkdirAll(l.Root(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Root(), "current"), []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, dir string, spec *apitypes.SyntheticSpec) (*driver, error) {
	t.Helper()
	ds, err := newDriver(context.Background(), pkgds.Spec{
		Name: catalog, Type: Type,
		Raw: map[string]any{"type": Type, "synthetic": spec, "syntheticDir": dir},
	})
	if err != nil {
		return nil, err
	}
	return ds.(*driver), nil
}

func mustOpen(t *testing.T, dir string, spec *apitypes.SyntheticSpec) *driver {
	t.Helper()
	d, err := open(t, dir, spec)
	if err != nil {
		t.Fatalf("newDriver: %v", err)
	}
	return d
}

func conn(t *testing.T) *sql.Conn {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func count(t *testing.T, c *sql.Conn) int {
	t.Helper()
	var n int
	if err := c.QueryRowContext(context.Background(), `SELECT count(*) FROM syn.crm.customers`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestFactoryRegisteredViaInit(t *testing.T) {
	if _, ok := pkgds.Lookup(Type); !ok {
		t.Fatal("synthetic driver did not self-register via init()")
	}
}

func TestNewDriverRequiresSpecAndDir(t *testing.T) {
	ctx := context.Background()
	for name, raw := range map[string]map[string]any{
		"no spec": {"syntheticDir": t.TempDir()},
		"no dir":  {"synthetic": testSpec(0.2)},
	} {
		if _, err := newDriver(ctx, pkgds.Spec{Name: catalog, Type: Type, Raw: raw}); err == nil {
			t.Errorf("%s: newDriver passed", name)
		}
	}
}

func TestNotBuilt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d := mustOpen(t, dir, testSpec(0.2))
	if got := d.State(); got != StateNotBuilt {
		t.Fatalf("State = %q; want %q", got, StateNotBuilt)
	}
	if !d.Readonly() || d.Type() != Type || d.Name() != catalog {
		t.Errorf("Readonly/Type/Name = %v/%q/%q", d.Readonly(), d.Type(), d.Name())
	}
	want := filepath.Join(dir, catalog, "snapshots") + string(filepath.Separator)
	if got := d.ExternalPaths(); len(got) != 1 || got[0] != want {
		t.Errorf("ExternalPaths = %v; want [%s]", got, want)
	}
	c := conn(t)
	for range 2 { // a second ATTACH is not an error
		if err := d.Attach(ctx, c, pkgds.AttachOptions{}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if err := d.HealthCheck(ctx, c, pkgds.HealthOptions{}); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
	sch, err := d.Schema(ctx, c)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if len(sch.Schemas) != 0 {
		t.Errorf("Schema = %+v; want no tables", sch)
	}
}

func TestReadyAndStale(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	spec := testSpec(0.2)
	l := build.Layout{Dir: dir, Name: catalog}
	writeBuild(t, l, build1, specHash(t, spec), 7)
	setCurrent(t, l, build1)

	d := mustOpen(t, dir, spec)
	if got := d.State(); got != StateReady {
		t.Fatalf("State = %q; want %q", got, StateReady)
	}
	c := conn(t)
	if err := d.Attach(ctx, c, pkgds.AttachOptions{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if n := count(t, c); n != 7 {
		t.Errorf("rows = %d; want 7", n)
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO syn.crm.customers VALUES (99)`); err == nil {
		t.Error("INSERT succeeded on a read-only attach")
	}
	sch, err := d.Schema(ctx, c)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if len(sch.Schemas) != 1 || sch.Schemas[0].Name != "crm" || len(sch.Schemas[0].Tables) != 1 {
		t.Errorf("Schema = %+v", sch)
	}

	// Another spec: the snapshot still serves, as stale.
	stale := mustOpen(t, dir, testSpec(0.1))
	if got := stale.State(); got != StateStale {
		t.Fatalf("State = %q; want %q", got, StateStale)
	}
	c2 := conn(t)
	if err := stale.Attach(ctx, c2, pkgds.AttachOptions{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if n := count(t, c2); n != 7 {
		t.Errorf("stale rows = %d; want 7", n)
	}
}

func TestCurrentNamesMissingFiles(t *testing.T) {
	dir := t.TempDir()
	l := build.Layout{Dir: dir, Name: catalog}
	setCurrent(t, l, build1)
	if _, err := open(t, dir, testSpec(0.2)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing snapshot: err = %v; want not exist", err)
	}
	writeBuild(t, l, build1, "sha256:x", 1)
	if err := os.Remove(l.Report(build1)); err != nil {
		t.Fatal(err)
	}
	if _, err := open(t, dir, testSpec(0.2)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing report: err = %v; want not exist", err)
	}
}

// Test 22, driver part: a bad id in current fails before any path use.
func TestInvalidCurrent(t *testing.T) {
	dir := t.TempDir()
	l := build.Layout{Dir: dir, Name: catalog}
	for _, id := range []string{"../x", "20261102T101500Z-A1B2C3", "latest", build1 + "/../../x"} {
		setCurrent(t, l, id)
		_, err := open(t, dir, testSpec(0.2))
		if !errors.Is(err, build.ErrInvalidBuildID) {
			t.Fatalf("current %q: err = %v; want invalid build id", id, err)
		}
		if !strings.Contains(err.Error(), "invalid build id") || strings.Contains(err.Error(), id) {
			t.Errorf("current %q: error text %q", id, err)
		}
	}
}

func TestSwap(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	spec := testSpec(0.2)
	hash := specHash(t, spec)
	l := build.Layout{Dir: dir, Name: catalog}
	d := mustOpen(t, dir, spec)
	c := conn(t)
	if err := d.Attach(ctx, c, pkgds.AttachOptions{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	writeBuild(t, l, build1, hash, 3)
	if got := d.BuildID(); got != "" {
		t.Errorf("BuildID before a swap = %q", got)
	}
	if err := d.Swap(ctx, c, build1, hash); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if got := d.State(); got != StateReady {
		t.Errorf("State = %q; want ready", got)
	}
	if got := d.BuildID(); got != build1 {
		t.Errorf("BuildID = %q; want %s", got, build1)
	}
	if n := count(t, c); n != 3 {
		t.Errorf("rows = %d; want 3", n)
	}

	// A failed ATTACH puts the old snapshot back and keeps the state.
	if err := d.Swap(ctx, c, build2, hash); err == nil {
		t.Fatal("Swap to a missing snapshot passed")
	}
	if got := d.State(); got != StateReady {
		t.Errorf("State after failed swap = %q; want ready", got)
	}
	if got := d.BuildID(); got != build1 {
		t.Errorf("BuildID after failed swap = %q; want %s", got, build1)
	}
	if n := count(t, c); n != 3 {
		t.Errorf("rows after failed swap = %d; want 3", n)
	}

	if err := d.Swap(ctx, c, "../x", hash); !errors.Is(err, build.ErrInvalidBuildID) {
		t.Errorf("Swap with a bad id: err = %v", err)
	}

	writeBuild(t, l, build2, "sha256:other", 5)
	if err := d.Swap(ctx, c, build2, "sha256:other"); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if got := d.State(); got != StateStale {
		t.Errorf("State = %q; want stale", got)
	}
	if n := count(t, c); n != 5 {
		t.Errorf("rows = %d; want 5", n)
	}

	// The old file is gone, so the fallback attaches an empty catalog.
	if err := os.Remove(l.Snapshot(build2)); err != nil {
		t.Fatalf("remove snapshot: %v", err)
	}
	if err := d.Swap(ctx, c, "20261102T121500Z-0a0b0c", hash); err == nil {
		t.Fatal("Swap to a missing snapshot passed")
	}
	if got := d.State(); got != StateNotBuilt {
		t.Errorf("State after failed fallback = %q; want not_built", got)
	}
	if got := d.BuildID(); got != "" {
		t.Errorf("BuildID after failed fallback = %q", got)
	}
	var one int
	q := `SELECT 1 FROM information_schema.schemata WHERE catalog_name = '` + catalog + `' LIMIT 1`
	if err := c.QueryRowContext(ctx, q).Scan(&one); err != nil {
		t.Errorf("catalog %s not attached after failed fallback: %v", catalog, err)
	}
}

// The swap runs after the hardening lock: the snapshot folder must be in
// allowed_directories, and new connections must see the new catalog.
func TestSwapThroughHardenedExecutor(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	spec := testSpec(0.2)
	hash := specHash(t, spec)
	l := build.Layout{Dir: dir, Name: catalog}
	d := mustOpen(t, dir, spec)
	exec, err := executor.New(ctx, executor.Config{
		Harden: executor.HardenConfig{AllowedExternalPaths: d.ExternalPaths()},
		AttachHook: func(ctx context.Context, conn *sql.Conn) error {
			return d.Attach(ctx, conn, pkgds.AttachOptions{})
		},
		MaxOpen: 2,
	})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = exec.Close() })

	writeBuild(t, l, build1, hash, 4)
	err = exec.Exclusive(ctx, func(ctx context.Context, conn *sql.Conn) error {
		return d.Swap(ctx, conn, build1, hash)
	})
	if err != nil {
		t.Fatalf("Exclusive swap: %v", err)
	}
	resp, err := exec.Execute(ctx, executor.Request{SQL: `SELECT count(*) FROM syn.crm.customers`})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()
	var n int
	if !resp.Rows.Next() {
		t.Fatalf("no row: %v", resp.Rows.Err())
	}
	if err := resp.Rows.Scan(&n); err != nil || n != 4 {
		t.Errorf("rows = %d, %v; want 4", n, err)
	}
}

func TestCloseClearsState(t *testing.T) {
	d := mustOpen(t, t.TempDir(), testSpec(0.2))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := d.State(); got != "" {
		t.Errorf("State after Close = %q", got)
	}
}
