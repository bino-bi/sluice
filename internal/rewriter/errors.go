// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"errors"

	"github.com/bino-bi/sluice/internal/schema"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

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

	// ErrSchemaMissing indicates the schema cache has no columns for a
	// table the rewrite must list: a masked table, or a star over a table
	// with a post-query mask.
	ErrSchemaMissing = errors.New("rewriter: table columns not in the schema cache")

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

// ErrorCode returns the client error code of an error from Rewrite: the
// code of an APIError in the chain, else the code of the sentinel.
// ErrSchemaMissing also wraps a schema load that failed or was canceled.
// Those keep their own codes, so only a table that a loaded catalog does
// not have is a rewrite failure. Any other error is CodeInternal.
func ErrorCode(err error) pkgerr.Code {
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeInternal {
		return ae.Code
	}
	switch {
	case errors.Is(err, ErrMaskPostQueryContext):
		return pkgerr.CodeMaskContext
	case errors.Is(err, ErrUnsupportedSyntax):
		return pkgerr.CodeUnsupportedSyntax
	case errors.Is(err, ErrDeparseFailed):
		return pkgerr.CodeRewriteFailed
	case errors.Is(err, ErrStatementRejected):
		return pkgerr.CodeACLRejected
	case errors.Is(err, context.Canceled):
		return pkgerr.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return pkgerr.CodeTimeout
	case errors.Is(err, schema.ErrUnknownCatalog), errors.Is(err, schema.ErrLoadFailed):
		return pkgerr.CodeDataSourceUnavailable
	case errors.Is(err, ErrSchemaMissing):
		return pkgerr.CodeRewriteFailed
	}
	return pkgerr.CodeInternal
}
