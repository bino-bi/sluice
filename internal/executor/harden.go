// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"database/sql"
	"fmt"
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

	// StatementTimeout is the per-statement wall-clock cap enforced by
	// DuckDB. 0 disables at this layer (context.Context still applies).
	StatementTimeoutMs int64
}

// hardeningStatements returns the ordered slice of SET statements
// applied to every fresh DuckDB connection. Ordering matters: the
// security knobs land first so a misconfigured tunable can't leave a
// connection partly-locked; lock_configuration goes last to freeze
// everything above it.
func hardeningStatements(cfg HardenConfig) []string {
	stmts := []string{
		// Security floor. These MUST land first.
		"SET enable_external_access=false",
		"SET allow_community_extensions=false",
		"SET autoinstall_known_extensions=false",
		"SET autoload_known_extensions=false",
		"SET allow_persistent_secrets=false",
	}
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
	// Statement timeout is enforced per-query via SET, not a session
	// default, because go-duckdb exposes no per-call knob. We leave the
	// session default at "no timeout" and apply Timeout on Execute.
	// Freeze configuration — nothing below this line can change settings
	// for the rest of the connection lifetime.
	stmts = append(stmts, "SET lock_configuration=true")
	return stmts
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
