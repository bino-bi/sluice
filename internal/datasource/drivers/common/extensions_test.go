// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

// openConn returns a connection to a fresh in-memory DuckDB with the
// extension repository pointed at a dead address and the extension
// directory at an empty temp dir, so any INSTALL that slips through
// fails fast instead of being satisfied by ~/.duckdb's cache or
// reaching extensions.duckdb.org.
func openConn(t *testing.T) *sql.Conn {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, stmt := range []string{
		`SET custom_extension_repository='http://127.0.0.1:9'`,
		`SET extension_directory='` + t.TempDir() + `'`,
	} {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return conn
}

func TestEnsureExtensionSkipsAlreadyLoaded(t *testing.T) {
	conn := openConn(t)
	// json is statically linked into every go-duckdb build, so the
	// probe must short-circuit before any INSTALL (which would fail
	// against the dead repository configured in openConn).
	if err := EnsureExtension(context.Background(), conn, "json"); err != nil {
		t.Fatalf("EnsureExtension(json) = %v, want nil", err)
	}
}

func TestEnsureExtensionInstallFailureSurfaces(t *testing.T) {
	conn := openConn(t)
	// httpfs is not loaded in default-binding builds; under
	// duckdb_use_static_lib it is statically linked and the probe
	// short-circuits, so only assert the INSTALL path when the
	// extension is genuinely absent.
	var loaded bool
	err := conn.QueryRowContext(context.Background(),
		"SELECT loaded FROM duckdb_extensions() WHERE extension_name = 'httpfs'",
	).Scan(&loaded)
	if err != nil {
		t.Fatalf("probe httpfs: %v", err)
	}
	got := EnsureExtension(context.Background(), conn, "httpfs")
	if loaded {
		if got != nil {
			t.Fatalf("EnsureExtension(httpfs) = %v, want nil for statically linked build", got)
		}
		return
	}
	if got == nil || !strings.Contains(got.Error(), "INSTALL httpfs") {
		t.Fatalf("EnsureExtension(httpfs) = %v, want INSTALL failure against dead repository", got)
	}
}
