// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !pure_parser

package parserbackend

import (
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
)

// Implemented reports whether this build's parser backend can actually
// parse SQL; false for the pure_parser stub.
const Implemented = true

// New returns the parser backend selected by the current build tags.
func New(opts parser.Options) parser.Parser {
	return pgquery.New(opts)
}

// Version identifies the active backend. Consumed by
// internal/version.PgQueryVersion() and the /v1/version response.
func Version() string { return pgquery.ParserVersion() }

// Name returns the backend identifier ("pg_query" or "cockroachdb").
func Name() string { return pgquery.Backend }
