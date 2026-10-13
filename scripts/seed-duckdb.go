// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build ignore

// seed-duckdb creates a DuckDB database file from a SQL file. It uses the
// DuckDB version sluice is built with, so a `duckdb_file` DataSource can
// attach the result. An existing output file is replaced.
//
// Usage: go run scripts/seed-duckdb.go <seed.sql> <out.duckdb>
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/seed-duckdb.go <seed.sql> <out.duckdb>")
		os.Exit(2)
	}
	if err := seed(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "seed-duckdb:", err)
		os.Exit(1)
	}
}

func seed(sqlFile, out string) error {
	script, err := os.ReadFile(sqlFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := sql.Open("duckdb", out)
	if err != nil {
		return err
	}
	if _, err := db.Exec(string(script)); err != nil {
		_ = db.Close()
		return err
	}
	return db.Close()
}
