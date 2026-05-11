// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
)

// FuzzParse feeds arbitrary input through the pg_query backend. The invariant
// is that Parse never panics and that any returned error matches one of the
// declared parser sentinels via errors.Is — otherwise we've surfaced a new
// error path that the transport / queryservice translation tables need to
// learn about.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"SELECT 1",
		"SELECT * FROM pg.public.t",
		"SELECT a, b FROM pg.public.t WHERE a = 1",
		"SELECT * FROM a JOIN b ON a.id = b.a_id",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		"SELECT 1; SELECT 2",
		"UPDATE t SET a = 1",
		"not a sql",
		"SELECT '", // unterminated literal
	}
	for _, s := range seeds {
		f.Add(s)
	}
	p := pgquery.New(parser.Options{})
	f.Fuzz(func(t *testing.T, sql string) {
		_, err := p.Parse(context.Background(), sql)
		if err == nil {
			return
		}
		if acceptableParseError(err) {
			return
		}
		t.Fatalf("unexpected parse error type %T: %v", err, err)
	})
}

// acceptableParseError returns true for every sentinel the transport /
// queryservice error tables already handle. An error outside this set is a
// fuzzer signal.
func acceptableParseError(err error) bool {
	if err == nil {
		return true
	}
	var pe *parser.ParseError
	if errors.As(err, &pe) {
		return true
	}
	switch {
	case errors.Is(err, parser.ErrSyntax),
		errors.Is(err, parser.ErrMultipleStatements),
		errors.Is(err, parser.ErrUnsupported),
		errors.Is(err, parser.ErrDeparseFailed),
		errors.Is(err, parser.ErrInputTooLarge),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return true
	}
	return false
}
