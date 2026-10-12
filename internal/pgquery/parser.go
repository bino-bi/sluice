// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

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
	// One scan feeds every token check. After a scan error the checks get
	// no tokens, and pg.Parse reports the syntax error.
	scan, scanErr := pg.Scan(sql)
	if err := checkDepth(scan.GetTokens()); err != nil {
		return nil, err
	}
	if err := checkIdentifierLength(sql, scan.GetTokens()); err != nil {
		return nil, err
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
	// The token checks could not run, so refuse rather than serve.
	if scanErr != nil {
		return nil, &parser.ParseError{Cause: scanErr}
	}
	if err := checkIdentifierRunes(sql, scan.GetTokens()); err != nil {
		return nil, &parser.ParseError{Cause: err}
	}
	if err := checkAliasKeywords(tree); err != nil {
		return nil, &parser.ParseError{Cause: err}
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

// checkIdentifierRunes refuses a statement whose unquoted identifiers
// contain a non-ASCII character that is not a letter, digit or mark.
// pg_query reads every byte >= 0x80 as part of an identifier, while DuckDB
// treats Unicode spaces as token separators. A statement that is served
// unchanged would then mean something else to the engine than to the
// checks that ran on the pg_query tree.
func checkIdentifierRunes(sql string, toks []*pg.ScanToken) error {
	for _, tok := range toks {
		if tok.Token != pg.Token_IDENT || int(tok.End) > len(sql) || tok.Start >= tok.End {
			continue
		}
		text := sql[tok.Start:tok.End]
		if strings.HasPrefix(text, `"`) {
			continue
		}
		for _, r := range text {
			if r < utf8.RuneSelf {
				continue
			}
			if r == utf8.RuneError {
				return parser.ErrIdentifierChars
			}
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
				return parser.ErrIdentifierChars
			}
		}
	}
	return nil
}

// maxIdentifierBytes is where pg_query cuts an identifier (NAMEDATALEN-1).
const maxIdentifierBytes = 63

// checkIdentifierLength refuses an identifier longer than 63 bytes.
// pg_query cuts it to 63 bytes and DuckDB does not, so a policy on the full
// name would not match the name policy sees, and a CTE named with the first
// 63 bytes would hide the table. A quoted name counts after decoding. A
// Unicode escape name (U&"...") counts its content as written, which is
// never shorter than its decoded form.
func checkIdentifierLength(sql string, toks []*pg.ScanToken) error {
	for _, tok := range toks {
		if int(tok.End) > len(sql) || tok.Start >= tok.End {
			continue
		}
		text := sql[tok.Start:tok.End]
		switch tok.Token {
		case pg.Token_IDENT:
			if len(text) >= 2 && text[0] == '"' {
				text = strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
			}
		case pg.Token_UIDENT:
			// Drop the U&" prefix and the closing quote.
			if len(text) >= 4 {
				text = text[3 : len(text)-1]
			}
		default:
			continue
		}
		if len(text) > maxIdentifierBytes {
			return parser.ErrIdentifierTooLong
		}
	}
	return nil
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
	scan, scanErr := pg.Scan(sql)
	if err := checkDepth(scan.GetTokens()); err != nil {
		return "", err
	}
	if err := checkIdentifierLength(sql, scan.GetTokens()); err != nil {
		return "", err
	}
	fp, err := pg.Fingerprint(sql)
	if err != nil {
		return "", &parser.ParseError{Cause: err}
	}
	if scanErr != nil {
		return "", &parser.ParseError{Cause: scanErr}
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
