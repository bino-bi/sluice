// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
)

// Backend returns "pg_query". Exported so callers can compare without
// importing the concrete parser type.
const Backend = "pg_query"

// Parser wraps pg_query_go/v6. It is safe for concurrent use: every call
// into pg_query_go creates its own native parser state, so there is no
// shared mutable data in this type.
type Parser struct {
	maxBytes int
	logger   *slog.Logger
}

// New returns a Parser configured from opts. Zero values fall back to
// parser.DefaultMaxSQLBytes and slog.Default.
func New(opts parser.Options) *Parser {
	p := &Parser{
		maxBytes: opts.MaxSQLBytes,
		logger:   opts.Logger,
	}
	if p.maxBytes <= 0 {
		p.maxBytes = parser.DefaultMaxSQLBytes
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// Name returns Backend.
func (p *Parser) Name() string { return Backend }

// Parse runs pg_query_go and wraps the result in an AST. Multi-statement
// inputs are rejected.
func (p *Parser) Parse(ctx context.Context, sql string) (parser.AST, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(sql) > p.maxBytes {
		return nil, parser.ErrInputTooLarge
	}

	tree, err := pg.Parse(sql)
	if err != nil {
		return nil, &parser.ParseError{Cause: err, Line: extractLine(sql, err)}
	}
	if tree == nil || len(tree.Stmts) == 0 {
		return nil, &parser.ParseError{Cause: errors.New("empty parse tree")}
	}
	if len(tree.Stmts) > 1 {
		return nil, parser.ErrMultipleStatements
	}

	fp, ferr := pg.Fingerprint(sql)
	if ferr != nil {
		return nil, fmt.Errorf("pg_query: fingerprint: %w", ferr)
	}

	stmt := tree.Stmts[0]
	kind := statementKind(stmt)
	a := &ast{
		raw:         tree,
		stmt:        stmt,
		source:      sql,
		fingerprint: fp,
		kind:        kind,
	}
	a.tables, a.catalogs = extractTables(stmt)
	a.shape = extractShape(stmt, a.tables, a.catalogs)
	return a, nil
}

// Deparse re-emits an AST. Only ASTs produced by this parser (or a clone
// of one) are supported.
func (p *Parser) Deparse(ctx context.Context, a parser.AST) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	native, ok := a.(*ast)
	if !ok {
		return "", fmt.Errorf("%w: foreign AST", parser.ErrDeparseFailed)
	}
	if native.raw == nil {
		return "", parser.ErrDeparseFailed
	}
	out, err := pg.Deparse(native.raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", parser.ErrDeparseFailed, err)
	}
	return out, nil
}

// Fingerprint returns the stable pg_query fingerprint for sql.
func (p *Parser) Fingerprint(sql string) (string, error) {
	if len(sql) > p.maxBytes {
		return "", parser.ErrInputTooLarge
	}
	fp, err := pg.Fingerprint(sql)
	if err != nil {
		return "", &parser.ParseError{Cause: err}
	}
	return fp, nil
}

// ParserVersion returns the pg_query protocol version. Wired into
// internal/version.PgQueryVersion().
func ParserVersion() string {
	// pg_query_go v6 does not expose a Version() symbol; the protocol
	// version is embedded on every ParseResult. A trivial parse produces
	// a deterministic value that identifies the backend.
	tree, err := pg.Parse("SELECT 1")
	if err != nil || tree == nil {
		return "unknown"
	}
	return fmt.Sprintf("pg_query_go/v6 proto=%d", tree.Version)
}

// extractLine does a best-effort extraction of the line number from
// pg_query's error message. pg_query_go returns "syntax error at … at …"
// without a structured offset. When nothing can be parsed, 0 is returned.
func extractLine(sql string, err error) int {
	// pg_query messages include "at or near …" but not a line number.
	// Count newlines up to the first offending token if we can find one.
	// As a cheap heuristic we return 0 — the Cause carries full detail
	// for logs.
	_ = sql
	_ = err
	return 0
}
