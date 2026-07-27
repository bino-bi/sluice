// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

// Regression tests for query-time reads through httpfs-backed views
// (2026-07 hardening spike). DuckDB's enable_external_access=false blocks
// read_csv/read_parquet at query time — including through views created
// during attach — unless the remote prefixes are carved out via
// allowed_directories. These tests pin three verdicts: the default stays
// fully closed, the carve-out admits exactly the configured prefixes
// (http:// and s3://), and allow_persistent_secrets lands at open time so
// drivers that CREATE SECRET during attach do not break hardening.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	duckdb "github.com/marcboeker/go-duckdb/v2"

	"github.com/bino-bi/sluice/internal/executor"
)

// ensureHTTPFS installs the httpfs extension once via a throwaway DuckDB
// instance, skipping the test when the extension cannot be fetched
// (offline dev machines). Later INSTALLs load from the on-disk cache.
func ensureHTTPFS(t *testing.T) {
	t.Helper()
	connector, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), "INSTALL httpfs"); err != nil {
		t.Skipf("INSTALL httpfs failed (offline?): %v", err)
	}
}

// serveDir exposes dir over a local HTTP server. http.FileServer answers
// HEAD and Range requests, which httpfs requires.
func serveDir(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return srv
}

func writeCSV(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// execStmts returns an AttachHook that runs the given statements.
func execStmts(stmts ...string) func(context.Context, *sql.Conn) error {
	return func(ctx context.Context, conn *sql.Conn) error {
		for _, s := range stmts {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("attach %q: %w", s, err)
			}
		}
		return nil
	}
}

func countQuery(t *testing.T, e *executor.Executor, query string) (int64, error) {
	t.Helper()
	resp, err := e.Execute(context.Background(), executor.Request{SQL: query})
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Rows.Close() }()
	var n int64
	if !resp.Rows.Next() {
		return 0, resp.Rows.Err()
	}
	if err := resp.Rows.Scan(&n); err != nil {
		return 0, err
	}
	return n, resp.Rows.Err()
}

func TestRemoteViewBlockedWithoutCarveOut(t *testing.T) {
	ensureHTTPFS(t)
	dir := t.TempDir()
	writeCSV(t, filepath.Join(dir, "t.csv"), "id,name\n1,alpha\n2,bravo\n")
	srv := serveDir(t, dir)

	e := newExec(t, executor.Config{
		AttachHook: execStmts(
			"INSTALL httpfs", "LOAD httpfs",
			"ATTACH ':memory:' AS ext",
			fmt.Sprintf("CREATE VIEW ext.main.t AS SELECT * FROM read_csv('%s/t.csv')", srv.URL),
		),
	})

	if _, err := countQuery(t, e, "SELECT count(*) FROM ext.main.t"); err == nil {
		t.Fatal("query through remote view succeeded without a carve-out; hardening is not default-closed")
	} else if !strings.Contains(err.Error(), "disabled by configuration") {
		t.Fatalf("err = %v; want DuckDB permission error", err)
	}
}

func TestRemoteViewReadableWithCarveOutHTTP(t *testing.T) {
	ensureHTTPFS(t)
	dir := t.TempDir()
	writeCSV(t, filepath.Join(dir, "t.csv"), "id,name\n1,alpha\n2,bravo\n")
	srv := serveDir(t, dir)

	e := newExec(t, executor.Config{
		Harden: executor.HardenConfig{
			AllowedExternalPaths: []string{srv.URL + "/"},
		},
		AttachHook: execStmts(
			"INSTALL httpfs", "LOAD httpfs",
			"ATTACH ':memory:' AS ext",
			fmt.Sprintf("CREATE VIEW ext.main.t AS SELECT * FROM read_csv('%s/t.csv')", srv.URL),
		),
	})

	n, err := countQuery(t, e, "SELECT count(*) FROM ext.main.t")
	if err != nil {
		t.Fatalf("query through carved-out view: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d; want 2", n)
	}

	// A URL outside the carve-out must stay blocked...
	if _, err := countQuery(t, e, "SELECT count(*) FROM read_csv('http://127.0.0.1:1/x.csv')"); err == nil {
		t.Fatal("read outside carve-out succeeded")
	}
	// ...and so must the local filesystem.
	local := filepath.Join(dir, "t.csv")
	if _, err := countQuery(t, e, fmt.Sprintf("SELECT count(*) FROM read_csv('%s')", local)); err == nil {
		t.Fatal("local filesystem read succeeded despite hardening")
	}
}

func TestRemoteViewReadableWithCarveOutS3(t *testing.T) {
	ensureHTTPFS(t)
	dir := t.TempDir()
	writeCSV(t, filepath.Join(dir, "bucket", "data.csv"), "id,name\n1,alpha\n2,bravo\n3,charlie\n")
	srv := serveDir(t, dir)
	endpoint := strings.TrimPrefix(srv.URL, "http://")

	e := newExec(t, executor.Config{
		Harden: executor.HardenConfig{
			AllowedExternalPaths: []string{"s3://bucket/"},
		},
		AttachHook: execStmts(
			"INSTALL httpfs", "LOAD httpfs",
			// The secret-manager touch during attach is the point: it must
			// not break the hardening pass (allow_persistent_secrets is
			// applied at open time).
			fmt.Sprintf("CREATE OR REPLACE SECRET reg_test (TYPE S3, KEY_ID 'k', SECRET 's', REGION 'us-east-1', ENDPOINT '%s', URL_STYLE 'path', USE_SSL 'false')", endpoint),
			"ATTACH ':memory:' AS ext",
			"CREATE VIEW ext.main.t AS SELECT * FROM read_csv('s3://bucket/data.csv')",
		),
	})

	n, err := countQuery(t, e, "SELECT count(*) FROM ext.main.t")
	if err != nil {
		t.Fatalf("query through s3 carve-out view: %v", err)
	}
	if n != 3 {
		t.Fatalf("count = %d; want 3", n)
	}

	if _, err := countQuery(t, e, "SELECT count(*) FROM read_csv('s3://otherbucket/x.csv')"); err == nil {
		t.Fatal("read outside s3 carve-out succeeded")
	}

	// allow_persistent_secrets must be false even though a secret was
	// created during attach — proves the open-time DSN placement.
	conn, err := e.DB().Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var persistent bool
	if err := conn.QueryRowContext(context.Background(),
		"SELECT value::BOOLEAN FROM duckdb_settings() WHERE name='allow_persistent_secrets'",
	).Scan(&persistent); err != nil {
		t.Fatalf("scan allow_persistent_secrets: %v", err)
	}
	if persistent {
		t.Fatal("allow_persistent_secrets = true; open-time hardening missing")
	}
}
