// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import "errors"

// Sentinel errors returned from Rewrite.
var (
	// ErrStatementRejected is returned when the statement kind is not
	// permitted at the transport boundary (write ops, DDL, COPY, ATTACH,
	// etc.). Maps to pkg/errors.CodeACLRejected for DML writes and
	// CodeUnsupportedSyntax for DDL/COPY/attach.
	ErrStatementRejected = errors.New("rewriter: statement kind rejected")

	// ErrUnsupportedSyntax marks SQL the rewriter refuses to touch. When
	// any policy matches, the request is rejected; when no policies
	// match, the queryservice may still pass it through.
	ErrUnsupportedSyntax = errors.New("rewriter: syntax cannot be rewritten safely")

	// ErrDeparseFailed wraps a backend deparse failure.
	ErrDeparseFailed = errors.New("rewriter: deparse failed")

	// ErrSchemaMissing indicates a SELECT * where the schema cache has no
	// columns for the target table.
	ErrSchemaMissing = errors.New("rewriter: schema not available for expansion")

	// ErrMaskUnsupported marks a mask type without a registered provider.
	ErrMaskUnsupported = errors.New("rewriter: mask provider not enabled")

	// ErrMaskPostQueryContext marks a post-query-masked column appearing
	// somewhere other than a bare top-level select item — a WHERE/HAVING/
	// JOIN/GROUP/ORDER predicate or a wrapping expression. Filtering or
	// computing on the raw value of an FPE/fake/jitter-masked column is an
	// inference oracle, so the request is refused. Maps to
	// pkg/errors.CodeMaskContext.
	ErrMaskPostQueryContext = errors.New("rewriter: post-query masked column used outside a bare select item")

	// ErrForeignAST indicates the AST did not come from the pg_query
	// backend — the MVP rewriter is pg_query-only.
	ErrForeignAST = errors.New("rewriter: AST is not from pg_query backend")
)
