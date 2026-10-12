// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by Parser implementations. At the transport
// boundary these are normalised to pkg/errors.APIError codes; the plain
// sentinels are matched via errors.Is inside the core.
var (
	ErrSyntax             = errors.New("parser: syntax error")
	ErrMultipleStatements = errors.New("parser: multiple statements not allowed")
	ErrUnsupported        = errors.New("parser: unsupported statement kind")
	ErrDeparseFailed      = errors.New("parser: deparse failed")
	ErrInputTooLarge      = errors.New("parser: input exceeds MaxSQLBytes")
	// ErrIdentifierChars reports an unquoted identifier that pg_query and
	// DuckDB would split into different tokens.
	ErrIdentifierChars = errors.New("unquoted identifier contains a non-letter Unicode character, quote the identifier")
	// ErrTooDeep reports a statement whose nesting would overflow the C
	// stack inside pg_query. Rejected before parsing.
	ErrTooDeep = errors.New("parser: statement nesting is too deep")
	// ErrIdentifierTooLong reports an identifier over 63 bytes. pg_query
	// cuts it to 63 bytes and DuckDB does not, so policy would see another
	// name than DuckDB reads.
	ErrIdentifierTooLong = errors.New("identifier longer than 63 bytes, use a shorter name or rename the object at the source")
	// ErrAliasKeyword reports a FROM alias that DuckDB reads as a keyword
	// (IsUnsafeBareAlias). pg_query reads it as an alias and deparses it
	// bare, so DuckDB would restructure the join or read the alias as a
	// clause, and skip the row filter and mask.
	ErrAliasKeyword = errors.New("table alias is a DuckDB keyword (such as semi, asof, qualify or pivot), use another alias")
	// ErrParamRef reports a $n in the caller's statement. Caller params are
	// not bound, and $n would read the values the rewriter binds: row filter
	// values and mask params such as a hash salt.
	ErrParamRef = errors.New("query parameters ($n) are not supported")
)

// ParseError carries position information for logs and audit records. The
// transport layer never returns ParseError to clients — it substitutes a
// generic CodeSyntax APIError — so leaking Cause here is safe.
type ParseError struct {
	Line  int
	Col   int
	Cause error
}

// Error formats ParseError for logs. Line and column are printed only when
// at least one is populated.
func (e *ParseError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Line > 0 || e.Col > 0 {
		return fmt.Sprintf("parser: syntax error at line %d col %d: %v", e.Line, e.Col, e.Cause)
	}
	return fmt.Sprintf("parser: syntax error: %v", e.Cause)
}

// Unwrap returns the underlying cause so callers can use errors.Is.
func (e *ParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is reports whether target matches ErrSyntax — a ParseError always
// represents a syntax error regardless of the specific Cause.
func (e *ParseError) Is(target error) bool {
	return target == ErrSyntax //nolint:errorlint // sentinel identity check is intentional
}
