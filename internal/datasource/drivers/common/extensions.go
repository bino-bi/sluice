// SPDX-License-Identifier: AGPL-3.0-or-later

package common

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EnsureExtension makes the DuckDB extension ext available on conn.
// When the extension is already loaded — statically compiled into the
// binary (build tag duckdb_use_static_lib) or LOADed earlier on this
// connection — nothing more happens. Otherwise a bare LOAD is tried
// first: it succeeds offline when the extension file is pre-seeded in
// the extension directory (the container images bake the non-embeddable
// extensions in via scripts/fetch-duckdb-extensions.sh), and DuckDB
// verifies the extension signature at LOAD time. Only when that fails
// does INSTALL run — the one step that downloads from
// extensions.duckdb.org. ext must be the canonical name reported by
// duckdb_extensions() (e.g. sqlite_scanner, not the sqlite alias).
func EnsureExtension(ctx context.Context, conn *sql.Conn, ext string) error {
	var loaded bool
	err := conn.QueryRowContext(ctx,
		"SELECT loaded FROM duckdb_extensions() WHERE extension_name = ?", ext,
	).Scan(&loaded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("probe extension %s: %w", ext, err)
	}
	if loaded {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "LOAD "+ext); err == nil {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "INSTALL "+ext); err != nil {
		return fmt.Errorf("INSTALL %s: %w", ext, err)
	}
	if _, err := conn.ExecContext(ctx, "LOAD "+ext); err != nil {
		return fmt.Errorf("LOAD %s: %w", ext, err)
	}
	return nil
}
