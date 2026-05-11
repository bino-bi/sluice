// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import "errors"

// Sentinel errors returned by Cache. Callers match them with errors.Is.
var (
	// ErrUnknownCatalog is returned when the Loader has no definition for
	// the requested catalog. The rewriter translates it to an
	// ERR_DATASOURCE_UNAVAILABLE client error.
	ErrUnknownCatalog = errors.New("schema: unknown catalog")

	// ErrUnknownTable is returned when a catalog loads successfully but
	// the requested (schema, table) is absent. If a policy matched the
	// missing table the rewriter escalates to an
	// ERR_UNSUPPORTED_SYNTAX_ON_PROTECTED_TABLE; otherwise the query is
	// passed through and DuckDB emits its own Catalog Error.
	ErrUnknownTable = errors.New("schema: unknown table")

	// ErrLoadFailed is returned when Loader.Load itself fails (network,
	// permission). The last cached Entry for the catalog is still served
	// with Entry.Stale=true.
	ErrLoadFailed = errors.New("schema: load failed")
)
