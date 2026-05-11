// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// OutputFormat names the serialisation the caller wants back. MVP
// supports JSON and CSV; Arrow streaming lands in a later slice.
type OutputFormat string

// Recognised output formats.
const (
	FormatJSON OutputFormat = "json"
	FormatCSV  OutputFormat = "csv"
	// FormatArrow is declared to keep the API surface stable. Execute
	// rejects it until the Arrow backend lands.
	FormatArrow OutputFormat = "arrow"
)

// Request carries a rewritten SQL statement plus the execution
// parameters derived from the policy engine.
type Request struct {
	// QueryID is the ULID assigned by the transport. Propagated into
	// logs and audit records.
	QueryID string

	// SQL is the post-rewrite statement, with `?` placeholders for
	// every bound value.
	SQL string

	// Params are the values bound to `?` placeholders, in order.
	Params []any

	// MaxRows caps the number of rows the RowIterator will yield.
	// 0 means "no cap" (the transport still enforces a size cap).
	MaxRows int64

	// Timeout is the wall-clock cap on this execute. Zero falls through
	// to Config.DefaultStatementTimeout.
	Timeout time.Duration

	// Format selects the RowIterator serialisation. Defaults to JSON.
	Format OutputFormat
}

// ColumnInfo reports a single result column's shape. Arrow metadata is
// absent until the Arrow backend lands.
type ColumnInfo struct {
	Name    string
	SQLType string
}

// Response wraps the result set iterator plus post-iteration accounting.
// RowCount is populated when Rows.Close is called; until then it is nil.
type Response struct {
	Columns   []ColumnInfo
	Rows      RowIterator
	RowCount  *int64
	Truncated bool
}

// RowIterator is a streaming result iterator. Callers must call Close
// after draining (defer is idiomatic). Scan's dest[] slice must match
// Columns in count and be compatible types.
type RowIterator interface {
	// Next advances to the next row. Returns false at EOF or on error;
	// check Err() afterwards.
	Next() bool

	// Scan populates dest[] with the current row's values. Must match
	// the column count reported in Response.Columns.
	Scan(dest ...any) error

	// Err returns the iteration error, if any.
	Err() error

	// Close releases resources held by the iterator. Calling Close more
	// than once returns nil.
	Close() error
}

// Execute runs req against a hardened connection drawn from the pool.
// Rows stream through the returned iterator; the underlying connection
// is held until Response.Rows.Close is called.
func (e *Executor) Execute(ctx context.Context, req Request) (*Response, error) {
	if req.Format == "" {
		req.Format = FormatJSON
	}
	switch req.Format {
	case FormatJSON, FormatCSV:
		// ok
	case FormatArrow:
		return nil, fmt.Errorf("%w: Arrow output not yet supported", ErrExecute)
	default:
		return nil, fmt.Errorf("%w: unknown output format %q", ErrExecute, req.Format)
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = e.cfg.DefaultStatementTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	// cancel runs when the iterator closes, not when this function
	// returns — otherwise we'd cancel the query mid-stream. Leak-proof
	// paths below call cancel on every early-exit branch.
	conn, err := e.db.Conn(execCtx)
	if err != nil {
		cancel()
		return nil, wrapRunErr(err)
	}
	if err := e.ensureInit(execCtx, conn); err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}

	rows, err := conn.QueryContext(execCtx, req.SQL, req.Params...)
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, wrapRunErr(err)
	}
	cols, err := rows.ColumnTypes()
	if err != nil {
		cancel()
		_ = rows.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("%w: columns: %w", ErrExecute, err)
	}
	colInfo := make([]ColumnInfo, len(cols))
	for i, c := range cols {
		colInfo[i] = ColumnInfo{Name: c.Name(), SQLType: c.DatabaseTypeName()}
	}

	rowCount := new(int64)
	iter := &sqlRowIterator{
		rows:     rows,
		conn:     conn,
		cancel:   cancel,
		maxRows:  req.MaxRows,
		rowCount: rowCount,
	}
	resp := &Response{
		Columns:  colInfo,
		Rows:     iter,
		RowCount: rowCount,
	}
	iter.parent = resp
	return resp, nil
}

// wrapRunErr maps database/sql errors onto the package sentinels so
// callers can switch on "kind" without unwrapping through multiple
// layers.
func wrapRunErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %w", ErrCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrExecute, err)
	default:
		return fmt.Errorf("%w: %w", ErrExecute, err)
	}
}

// sqlRowIterator is the JSON/CSV RowIterator backed by *sql.Rows. Scan
// delegates straight through so callers get Go-native types (int64,
// float64, string, time.Time, []byte, bool, nil). The transport layer
// converts to the wire format.
type sqlRowIterator struct {
	rows     *sql.Rows
	conn     *sql.Conn
	cancel   context.CancelFunc
	maxRows  int64
	rowCount *int64
	parent   *Response
	closed   bool
	iterErr  error
}

// Next advances the iterator, tracking the row count and enforcing
// MaxRows.
func (it *sqlRowIterator) Next() bool {
	if it.closed || it.iterErr != nil {
		return false
	}
	if it.maxRows > 0 && *it.rowCount >= it.maxRows {
		it.parent.Truncated = true
		return false
	}
	if !it.rows.Next() {
		return false
	}
	*it.rowCount++
	return true
}

// Scan forwards to the underlying rows.
func (it *sqlRowIterator) Scan(dest ...any) error {
	if it.closed {
		return errors.New("executor: Scan after Close")
	}
	if err := it.rows.Scan(dest...); err != nil {
		it.iterErr = fmt.Errorf("%w: scan: %w", ErrExecute, err)
		return it.iterErr
	}
	return nil
}

// Err reports the iteration error if any.
func (it *sqlRowIterator) Err() error {
	if it.iterErr != nil {
		return it.iterErr
	}
	if err := it.rows.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrExecute, err)
	}
	return nil
}

// Close releases the rows + conn and cancels the per-request context.
// Idempotent.
func (it *sqlRowIterator) Close() error {
	if it.closed {
		return nil
	}
	it.closed = true
	var firstErr error
	if err := it.rows.Close(); err != nil {
		firstErr = fmt.Errorf("%w: rows close: %w", ErrExecute, err)
	}
	if err := it.conn.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("%w: conn close: %w", ErrExecute, err)
	}
	// Cancel the per-request context last so a lingering query sees a
	// cancellation rather than a sudden connection close.
	it.cancel()
	return firstErr
}
