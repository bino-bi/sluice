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

	// AllowedExternalPaths lists remote URI prefixes (e.g. "s3://bucket/",
	// "https://host/data/") that query-time reads may touch. They are
	// rendered into DuckDB's allowed_directories, which carves exceptions
	// out of enable_external_access=false — verified against DuckDB 1.3:
	// reads under a listed prefix succeed while everything else (other
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
		stmts = append(stmts, allowedDirectoriesStatement(cfg))
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

// allowedDirectoriesStatement renders the allowed_directories carve-out:
// the configured remote prefixes plus the temp-spill directory (DuckDB's
// own default for allowed_directories is [<temp_directory>/], which a
// plain SET would silently drop, breaking larger-than-memory queries).
func allowedDirectoriesStatement(cfg HardenConfig) string {
	temp := cfg.TempDirectory
	if temp == "" {
		temp = ".tmp/" // DuckDB's own default allowed_directories entry
	}
	seen := map[string]bool{}
	paths := []string{temp}
	seen[temp] = true
	for _, p := range cfg.AllowedExternalPaths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths[1:]) // deterministic statement; temp entry stays first
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + escapeSQLLiteral(p) + "'"
	}
	return "SET allowed_directories=[" + strings.Join(quoted, ", ") + "]"
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
