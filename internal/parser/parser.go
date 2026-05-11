// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"context"
	"log/slog"
)

// DefaultMaxSQLBytes is the default cap on input SQL length enforced by
// Parse before dispatching to the backend. Requests over this size are
// rejected to keep parser CPU time bounded on pathological inputs.
const DefaultMaxSQLBytes = 1 << 20 // 1 MiB

// Options configures a Parser at construction time.
type Options struct {
	// MaxSQLBytes caps input size; zero means DefaultMaxSQLBytes.
	MaxSQLBytes int

	// Logger receives parser diagnostics. Nil uses slog.Default.
	Logger *slog.Logger
}

// Parser parses, deparses, and fingerprints SQL. Implementations are
// goroutine-safe.
type Parser interface {
	// Parse returns the AST for sql. Multiple statements produce
	// ErrMultipleStatements; oversized input produces ErrInputTooLarge;
	// syntax errors produce a *ParseError wrapping ErrSyntax.
	Parse(ctx context.Context, sql string) (AST, error)

	// Deparse re-emits ast as SQL. May return ErrDeparseFailed for ASTs
	// the backend cannot round-trip.
	Deparse(ctx context.Context, ast AST) (string, error)

	// Fingerprint returns a stable hex fingerprint of sql that is
	// insensitive to literal values — used for audit grouping and the
	// v1 rewrite cache key.
	Fingerprint(sql string) (string, error)

	// Name identifies the backend ("pg_query" or "cockroachdb").
	Name() string
}
