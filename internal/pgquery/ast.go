// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery

import (
	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"

	"github.com/bino-bi/sluice/internal/parser"
)

// ast is the pg_query implementation of parser.AST. It holds the raw
// ParseResult (for Deparse and Clone) plus precomputed derivations so
// callers pay the walk cost once.
type ast struct {
	raw         *pg.ParseResult
	stmt        *pg.RawStmt
	source      string
	fingerprint string
	kind        parser.StmtKind
	tables      []parser.TableRef
	catalogs    []string
	shape       parser.QueryShape
}

// Raw returns the backend-specific parse tree. Opaque to callers outside
// this package.
func (a *ast) Raw() any { return a.raw }

// Fingerprint returns the precomputed pg_query fingerprint.
func (a *ast) Fingerprint() string { return a.fingerprint }

// Tables returns the table references collected at parse time. The slice
// is shared with the AST — callers must treat it as immutable.
func (a *ast) Tables() []parser.TableRef { return a.tables }

// Catalogs returns the distinct non-empty Catalog values seen in Tables().
func (a *ast) Catalogs() []string { return a.catalogs }

// Shape returns the precomputed query shape.
func (a *ast) Shape() parser.QueryShape { return a.shape }

// Statement returns the top-level statement kind.
func (a *ast) Statement() parser.StmtKind { return a.kind }

// Source returns the original SQL text.
func (a *ast) Source() string { return a.source }

// Clone returns a deep copy via proto.Clone, recomputing only the proto
// pointers. Derivations (tables, shape, fingerprint) are immutable and
// safe to share.
func (a *ast) Clone() parser.AST {
	if a == nil || a.raw == nil {
		return nil
	}
	cloned, _ := proto.Clone(a.raw).(*pg.ParseResult)
	var stmt *pg.RawStmt
	if cloned != nil && len(cloned.Stmts) > 0 {
		stmt = cloned.Stmts[0]
	}
	return &ast{
		raw:         cloned,
		stmt:        stmt,
		source:      a.source,
		fingerprint: a.fingerprint,
		kind:        a.kind,
		tables:      a.tables,
		catalogs:    a.catalogs,
		shape:       a.shape,
	}
}
