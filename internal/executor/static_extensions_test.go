// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build duckdb_use_static_lib

package executor

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/datasource/drivers/common"
)

// staticExtensions are compiled into the binary by the custom DuckDB
// bundle (scripts/build-libduckdb.sh) when built with
// -tags duckdb_use_static_lib. The postgres/mysql scanners are absent
// by necessity (symbol collision with pg_query_go, no static target —
// see the build script), so those drivers keep the INSTALL/LOAD path.
var staticExtensions = []string{
	"httpfs", "excel", "sqlite_scanner",
}

// TestStaticExtensionsLinked proves the bundle delivered what it
// promises: every driver extension is loaded at startup without any
// INSTALL. The probe runs on a raw (pre-hardening) connection because
// duckdb_extensions() scans the extension directory on disk, which
// both requires external access and — on a developer machine with a
// warm ~/.duckdb cache — would mislabel install_mode as REPOSITORY;
// pointing extension_directory at an empty temp dir isolates it.
func TestStaticExtensionsLinked(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(),
		`SET extension_directory='`+t.TempDir()+`'`); err != nil {
		t.Fatalf("set extension_directory: %v", err)
	}

	for _, ext := range staticExtensions {
		var loaded bool
		var mode string
		err := db.QueryRowContext(context.Background(),
			"SELECT loaded, install_mode FROM duckdb_extensions() WHERE extension_name = ?", ext,
		).Scan(&loaded, &mode)
		if err != nil {
			t.Fatalf("probe %s: %v", ext, err)
		}
		if !loaded || mode != "STATICALLY_LINKED" {
			t.Errorf("%s: loaded=%v install_mode=%q, want loaded=true install_mode=STATICALLY_LINKED", ext, loaded, mode)
		}
	}
}

// TestEnsureExtensionNoNetworkForStatic proves attach-time extension
// setup never issues INSTALL for bundled extensions: with the extension
// repository pointed at a dead address and an empty extension directory,
// any INSTALL would fail fast — success means it was skipped.
func TestEnsureExtensionNoNetworkForStatic(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	for _, stmt := range []string{
		`SET custom_extension_repository='http://127.0.0.1:9'`,
		`SET extension_directory='` + t.TempDir() + `'`,
	} {
		if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for _, ext := range staticExtensions {
		if err := common.EnsureExtension(context.Background(), conn, ext); err != nil {
			t.Errorf("EnsureExtension(%s) = %v, want nil (INSTALL must be skipped)", ext, err)
		}
	}
}

// TestStaticDuckDBVersionMatchesPin keeps scripts/DUCKDB_VERSION honest
// at runtime level: the engine the binary embeds must be the release
// the bundle was built from.
func TestStaticDuckDBVersionMatchesPin(t *testing.T) {
	pin, err := os.ReadFile("../../scripts/DUCKDB_VERSION")
	if err != nil {
		t.Fatalf("read version pin: %v", err)
	}
	e, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatalf("executor.New: %v", err)
	}
	defer e.Close()
	var version string
	if err := e.DB().QueryRowContext(context.Background(), "SELECT version()").Scan(&version); err != nil {
		t.Fatalf("version(): %v", err)
	}
	if want := strings.TrimSpace(string(pin)); version != want {
		t.Errorf("version() = %q, want %q", version, want)
	}
}
