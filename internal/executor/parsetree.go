// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/parser"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Parse instance sizing. json_serialize_sql of a 1 MiB IN list needs about
// 400 MiB of DuckDB memory for the two trees.
const (
	parseCheckConns       = 2
	parseCheckMemPerByte  = 512
	minParseCheckMemLimit = 256 << 20
)

// NewParseCheck opens the DuckDB instance of the rewriter's parse check:
// one thread, nothing attached, two connections, and a memory limit that
// holds two checks of maxSQLBytes each (at least 256 MiB). maxSQLBytes <= 0
// means parser.DefaultMaxSQLBytes.
func NewParseCheck(ctx context.Context, maxSQLBytes int, logger *slog.Logger) (*Executor, error) {
	if maxSQLBytes <= 0 {
		maxSQLBytes = parser.DefaultMaxSQLBytes
	}
	limit := max(minParseCheckMemLimit, int64(parseCheckConns)*parseCheckMemPerByte*int64(maxSQLBytes))
	return New(ctx, Config{
		Harden:  HardenConfig{Threads: 1, MemoryLimit: fmt.Sprintf("%dMiB", limit>>20)},
		MaxOpen: parseCheckConns,
		MaxIdle: parseCheckConns,
		Logger:  logger,
	})
}

// parseTreesSQL serializes two statements with DuckDB's own parser. The
// cast types the parameters; an untyped one fails to bind.
const parseTreesSQL = "SELECT json_serialize_sql($1::VARCHAR)::VARCHAR, json_serialize_sql($2::VARCHAR)::VARCHAR"

// ParseTrees returns DuckDB's parse trees of a and b as json_serialize_sql
// JSON. A statement DuckDB cannot parse yields a tree that starts with
// {"error":true. Both texts are bound as values and never run. An
// out-of-memory error carries its client code and message, so callers map
// it like any other APIError.
func (e *Executor) ParseTrees(ctx context.Context, a, b string) (string, string, error) {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", "", wrapRunErr(err)
		}
		return "", "", fmt.Errorf("%w: %w", ErrConnUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	if err := e.ensureInit(ctx, conn); err != nil {
		return "", "", err
	}
	var treeA, treeB string
	if err := conn.QueryRowContext(ctx, parseTreesSQL, a, b).Scan(&treeA, &treeB); err != nil {
		if de := (*duckdb.Error)(nil); errors.As(err, &de) && de.Type == duckdb.ErrorTypeOutOfMemory {
			return "", "", pkgerr.Wrap(pkgerr.CodePayloadTooLarge, fmt.Errorf("%w: %w", ErrOutOfMemory, err)).WithMessage(
				"statement too large for the DuckDB parse check; send a shorter statement (for example a shorter IN list) or retry later")
		}
		return "", "", wrapRunErr(err)
	}
	return treeA, treeB, nil
}
