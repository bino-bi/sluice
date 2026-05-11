// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/bino-bi/sluice/internal/datasource"
	_ "github.com/bino-bi/sluice/internal/datasource/drivers/sqlitefile"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestGateSelectOneThroughSQLiteAttach is the Slice 2 gate test: a
// SELECT flowing through the executor pool with the sqlitefile driver
// installed via the datasource.Registry AttachHook. This exercises the
// full slice-1 ↔ slice-2 wiring: parser-free query (SELECT 1 + join
// against an attached catalog), hardening applied, driver ATTACH'd.
func TestGateSelectOneThroughSQLiteAttach(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "gate.sqlite")
	createSQLiteFixture(t, dbPath)

	ds := &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.io/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: "fixture"},
		Spec: apitypes.DataSourceSpec{
			Type:     apitypes.DSSQLite,
			Path:     dbPath,
			Readonly: true,
		},
	}

	reg, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{ds}},
		HealthInterval: -1, // disable the loop in tests
		FailFast:       true,
	})
	if err != nil {
		t.Fatalf("datasource.New: %v", err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	e, err := executor.New(context.Background(), executor.Config{
		AttachHook: reg.AttachHook(),
	})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	// Query the attached catalog.
	resp, err := e.Execute(context.Background(), executor.Request{
		SQL: "SELECT id, name FROM fixture.main.widgets ORDER BY id",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = resp.Rows.Close() }()

	type row struct {
		id   int64
		name string
	}
	var got []row
	for resp.Rows.Next() {
		var r row
		if err := resp.Rows.Scan(&r.id, &r.name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := resp.Rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	want := []row{{1, "alpha"}, {2, "bravo"}, {3, "charlie"}}
	if len(got) != len(want) {
		t.Fatalf("got %d rows; want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// createSQLiteFixture writes a tiny SQLite database at path with a
// single table "widgets" populated with three rows. We reach out to
// modernc.org/sqlite (pure Go, no CGo) to avoid a sqlite3 CGo build
// just for test fixtures.
func createSQLiteFixture(t *testing.T, path string) {
	t.Helper()
	// Touch the file so modernc.org/sqlite creates a new database rather
	// than an error.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write fixture path: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	stmts := []string{
		`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO widgets (id, name) VALUES (1, 'alpha'), (2, 'bravo'), (3, 'charlie')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}
