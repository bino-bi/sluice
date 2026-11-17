// SPDX-License-Identifier: AGPL-3.0-or-later

package schema_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/schema"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
)

type stubSource struct{ err error }

func (stubSource) Name() string   { return "pg" }
func (stubSource) Type() string   { return "stub" }
func (stubSource) Readonly() bool { return true }
func (stubSource) Attach(context.Context, *sql.Conn, pkgds.AttachOptions) error {
	return nil
}
func (s stubSource) Schema(context.Context, *sql.Conn) (pkgds.Schema, error) {
	return pkgds.Schema{Catalog: "pg"}, s.err
}
func (stubSource) HealthCheck(context.Context, *sql.Conn, pkgds.HealthOptions) error { return nil }
func (stubSource) Close() error                                                      { return nil }

type markingResolver struct {
	ds     pkgds.DataSource
	pulled map[string]time.Time
}

func (r *markingResolver) Lookup(string) (pkgds.DataSource, error) { return r.ds, nil }
func (r *markingResolver) MarkSchemaPulled(catalog string, at time.Time) {
	r.pulled[catalog] = at
}

func TestLoaderMarksSchemaPulled(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	failing := &markingResolver{ds: stubSource{err: errors.New("down")}, pulled: map[string]time.Time{}}
	if _, err := schema.NewLoader(failing, db).Load(context.Background(), "pg"); err == nil {
		t.Fatal("Load: want error")
	}
	if len(failing.pulled) != 0 {
		t.Errorf("failed load marked the schema as pulled: %v", failing.pulled)
	}

	ok := &markingResolver{ds: stubSource{}, pulled: map[string]time.Time{}}
	if _, err := schema.NewLoader(ok, db).Load(context.Background(), "pg"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok.pulled["pg"].IsZero() {
		t.Error("successful load did not mark the schema as pulled")
	}
}
