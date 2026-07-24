// SPDX-License-Identifier: AGPL-3.0-or-later

package executor

import "errors"

// Sentinel errors. Every public entry point wraps one so upstream code
// can switch on the kind without digging through wrapping chains.
var (
	// ErrOpen is returned when the underlying DuckDB instance cannot be
	// opened (bad path, CGo linker trouble, etc.).
	ErrOpen = errors.New("executor: cannot open duckdb")

	// ErrHardenFailed is returned when one of the SET statements in the
	// connection-init sequence fails. Connections that trip this error
	// are discarded before they reach user traffic.
	ErrHardenFailed = errors.New("executor: hardening failed")

	// ErrAttach is returned when the AttachHook installed by the
	// datasource registry fails on a connection.
	ErrAttach = errors.New("executor: attach hook failed")

	// ErrExecute wraps errors surfaced by DuckDB during query execution.
	ErrExecute = errors.New("executor: query execution failed")

	// ErrConnUnavailable is returned when a hardened connection cannot be
	// drawn from the pool for reasons other than the caller's context.
	// Maps to ERR_DATASOURCE_UNAVAILABLE upstream.
	ErrConnUnavailable = errors.New("executor: connection unavailable")

	// ErrCanceled is returned when a context is canceled during
	// execution. It wraps context.Canceled so callers can use
	// errors.Is(err, context.Canceled) if they prefer.
	ErrCanceled = errors.New("executor: canceled")
)
