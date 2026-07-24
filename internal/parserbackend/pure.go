// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build pure_parser

package parserbackend

import (
	"context"
	"errors"

	"github.com/bino-bi/sluice/internal/parser"
)

// errPureNotImplemented is returned by every method of the stub backend
// until the cockroachdb-parser implementation lands (v2 roadmap).
var errPureNotImplemented = errors.New("parser: pure_parser backend not yet implemented — build without -tags=pure_parser")

// Implemented reports whether this build's parser backend can actually
// parse SQL; false for the pure_parser stub.
const Implemented = false

type stub struct{}

func (stub) Parse(context.Context, string) (parser.AST, error) { return nil, errPureNotImplemented }
func (stub) Deparse(context.Context, parser.AST) (string, error) {
	return "", errPureNotImplemented
}
func (stub) Fingerprint(string) (string, error) { return "", errPureNotImplemented }
func (stub) Name() string                       { return "cockroachdb" }

// New returns the pure-Go backend stub.
func New(_ parser.Options) parser.Parser { return stub{} }

// Version identifies the stub.
func Version() string { return "cockroachdb-parser (not yet implemented)" }

// Name returns the backend identifier.
func Name() string { return "cockroachdb" }
