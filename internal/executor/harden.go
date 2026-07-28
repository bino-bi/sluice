// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// HardenConfig captures the user-tunable parameters applied during the
// connection-init sequence. Zero values keep DuckDB defaults for that
// specific setting — except for the "must-be-set" security knobs, which
// are always applied.
type HardenConfig struct {
	// MemoryLimit accepts DuckDB-formatted strings such as "4GB" or
	// "512MB". Empty leaves DuckDB's default.
	MemoryLimit string

	// Threads is the DuckDB worker thread count. 0 leaves the default
	// (pick based on logical CPU count).
	Threads int

	// TempDirectory is an absolute path for spilled-to-disk temp files.
	// Empty keeps DuckDB's default (OS temp).
	TempDirectory string

	// AllowedExternalPaths lists remote URIs that query-time reads may
	// touch. Entries with a trailing slash are treated as prefixes and
	// rendered into DuckDB's allowed_directories (e.g. "s3://bucket/");
	// entries without one are exact files rendered into allowed_paths
	// (e.g. "https://host/data/report.csv"). Both carve exceptions out
	// of enable_external_access=false — verified against DuckDB 1.5:
	// reads of a listed location succeed while everything else (other
	// URLs, the local filesystem) stays blocked. Empty keeps today's
	// fully-closed posture and emits no statement at all.
	AllowedExternalPaths []string
}

// hardeningStatements returns the ordered slice of SET statements
// applied to every fresh DuckDB connection. Ordering matters: the
// security knobs land first so a misconfigured tunable can't leave a
// connection partly-locked; lock_configuration goes last to freeze
// everything above it.
func hardeningStatements(cfg HardenConfig) []string {
	var stmts []string
	// The carve-out must be declared before external access is disabled;
	// DuckDB evaluates allowed_directories only while
	// enable_external_access=false.
	if len(cfg.AllowedExternalPaths) > 0 {
		stmts = append(stmts, carveOutStatements(cfg)...)
	}
	stmts = append(stmts,
		// Security floor. These MUST land first.
		"SET enable_external_access=false",
		"SET allow_community_extensions=false",
		"SET autoinstall_known_extensions=false",
		"SET autoload_known_extensions=false",
		// allow_persistent_secrets=false is applied at database-open time
		// (see the DSN in New): httpfs initialises the secret manager as
		// soon as a driver binds a remote path during attach, and DuckDB
		// rejects changing secret-manager settings after first use.
	)
	// User tunables — only emit a statement when a value is present so
	// DuckDB's own default remains authoritative otherwise.
	if cfg.MemoryLimit != "" {
		stmts = append(stmts, fmt.Sprintf("SET memory_limit='%s'", escapeSQLLiteral(cfg.MemoryLimit)))
	}
	if cfg.Threads > 0 {
		stmts = append(stmts, fmt.Sprintf("SET threads=%d", cfg.Threads))
	}
	if cfg.TempDirectory != "" {
		stmts = append(stmts, fmt.Sprintf("SET temp_directory='%s'", escapeSQLLiteral(cfg.TempDirectory)))
	}
	// Per-request timeouts are enforced via the Go context deadline on
	// Execute (the go-duckdb driver turns deadline expiry into a
	// duckdb_interrupt); DuckDB has no statement_timeout setting to emit.
	// Freeze configuration — nothing below this line can change settings
	// for the rest of the connection lifetime.
	stmts = append(stmts, "SET lock_configuration=true")
	return stmts
}

// carveOutStatements renders the external-access carve-outs. Trailing-
// slash entries become allowed_directories prefixes — always including
// the temp-spill directory, because DuckDB's own default for
// allowed_directories is [<temp_directory>/] and a plain SET would
// silently drop it, breaking larger-than-memory queries. Exact-file
// entries become allowed_paths; when none exist the statement is
// omitted so DuckDB's default (empty) stands.
func carveOutStatements(cfg HardenConfig) []string {
	seen := map[string]bool{}
	var dirs, files []string
	for _, p := range cfg.AllowedExternalPaths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if strings.HasSuffix(p, "/") {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)

	temp := cfg.TempDirectory
	if temp == "" {
		temp = ".tmp/" // DuckDB's own default allowed_directories entry
	}
	stmts := []string{"SET allowed_directories=" + sqlStringList(append([]string{temp}, dirs...))}
	if len(files) > 0 {
		stmts = append(stmts, "SET allowed_paths="+sqlStringList(files))
	}
	return stmts
}

// sqlStringList renders escaped, single-quoted entries as a DuckDB list
// literal.
func sqlStringList(entries []string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = "'" + escapeSQLLiteral(e) + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// applyHardening runs each SET against conn. A failure on any step
// returns a wrapped ErrHardenFailed — the caller is expected to discard
// the connection (DuckDB may have partly-applied changes).
func applyHardening(ctx context.Context, conn *sql.Conn, cfg HardenConfig) error {
	for _, stmt := range hardeningStatements(cfg) {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w: %q: %w", ErrHardenFailed, stmt, err)
		}
	}
	return nil
}

// escapeSQLLiteral doubles single quotes so a path or identifier can be
// safely embedded in a SQL string literal.
func escapeSQLLiteral(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
