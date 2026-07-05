// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"context"
	"errors"
)

// ErrPostQueryOnly is returned by MaskSQL for providers that can only
// mask values after execution (FPE, fake, jitter). The rewriter routes
// such providers through the RowMasker path instead of SQL substitution.
var ErrPostQueryOnly = errors.New("mask: provider masks post-query; MaskSQL not supported")

// RowMasker is implemented by providers that transform values in Go after
// the query executes, rather than emitting a SQL expression. The rewriter
// leaves the column reference intact and records the result-column index;
// queryservice builds one RowMask per masked column and applies it to
// every scanned value.
type RowMasker interface {
	Provider
	// NewRowMask returns a per-query masker for one column. It is called
	// once per query per masked column; the returned RowMask is used
	// sequentially for every row and need not be safe for concurrent use.
	NewRowMask(ctx RowMaskContext) (RowMask, error)
}

// RowMask masks a single column's value. Implementations must handle a
// nil value (SQL NULL) by returning nil.
type RowMask interface {
	Mask(value any) (any, error)
}

// PathChooser lets a provider that supports both a SQL and a post-query
// path decide per-args which one applies (e.g. hash: sha256 is SQL,
// hmac_sha256 is post-query). Providers without this interface are routed
// purely by whether they implement RowMasker.
type PathChooser interface {
	PostQuery(args Args) bool
}

// RowMaskContext carries the inputs for NewRowMask.
type RowMaskContext struct {
	Ctx      context.Context
	Column   ColumnRef
	Args     Args
	Identity Identity
	Keys     KeyStore
	Salts    SaltStore
}

// IsPostQuery reports whether provider p masks column values after query
// execution for the given args. A provider is post-query if it implements
// RowMasker and either does not implement PathChooser or its PathChooser
// selects the post-query path.
func IsPostQuery(p Provider, args Args) bool {
	rm, ok := p.(RowMasker)
	if !ok {
		return false
	}
	if pc, ok := rm.(PathChooser); ok {
		return pc.PostQuery(args)
	}
	return true
}
