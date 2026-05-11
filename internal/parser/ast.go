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

// TableRef is a reference to a table inside an AST. Catalog is empty when
// the query used an unqualified or two-part name; the rewriter fills it in
// from the default catalog.
type TableRef struct {
	Catalog string
	Schema  string
	Table   string
	Alias   string
	// Line is a best-effort source line for nicer diagnostics. Zero when
	// the backend does not expose position info.
	Line int
}
