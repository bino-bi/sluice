// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// AST is the narrow interface the rest of the system consumes. Concrete
// backends wrap their native node tree; callers must not unwrap Raw()
// outside the backend's own package.
type AST interface {
	// Raw returns the backend-specific node tree. Opaque to callers.
	Raw() any

	// Fingerprint returns the pre-computed fingerprint for this AST.
	Fingerprint() string

	// Tables returns the table references discovered during parsing.
	// CTE names are not included. Results are deterministic.
	Tables() []TableRef

	// Catalogs returns the distinct Catalog values from Tables(), in the
	// order of first occurrence. Empty catalog is omitted.
	Catalogs() []string

	// Shape summarises query structure consumed by policy rejects.
	Shape() QueryShape

	// Clone returns a deep copy suitable for rewriting without mutating
	// the original.
	Clone() AST

	// Statement returns the top-level statement kind.
	Statement() StmtKind

	// Source returns the original SQL string for audit / pass-through.
	Source() string
}

// DuckDB reads a table written without a catalog in its default database,
// and one written without a schema in its default schema. Sluice runs
// DuckDB in memory, so both are fixed. TestDefaultDatabaseAndSchema in
// internal/executor checks them.
const (
	DefaultDatabase = "memory"
	DefaultSchema   = "main"
)

// TableRef is a reference to a table inside an AST. Catalog and Schema are
// empty when the query did not write them. Policy selectors then match
// DefaultDatabase and DefaultSchema, the names DuckDB reads.
type TableRef struct {
	Catalog string
	Schema  string
	Table   string
	Alias   string
	// Line is a best-effort source line for nicer diagnostics. Zero when
	// the backend does not expose position info.
	Line int
}
