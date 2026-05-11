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
