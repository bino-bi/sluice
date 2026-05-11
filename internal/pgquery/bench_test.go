// SPDX-License-Identifier: AGPL-3.0-or-later

package pgquery_test

import (
	"context"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
)

const benchSQL = `SELECT o.id, c.email, c.ssn ` +
	`FROM pg.public.orders o JOIN pg.public.customers c ON o.customer_id = c.id ` +
	`WHERE o.tenant_id = 't-1' AND o.total > 100 ` +
	`ORDER BY o.id DESC LIMIT 100`

// BenchmarkParse measures pg_query cold-start parse cost. Used as a
// baseline for release-to-release regression checks (plan 24 §10).
func BenchmarkParse(b *testing.B) {
	p := pgquery.New(parser.Options{})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.Parse(ctx, benchSQL); err != nil {
			b.Fatalf("parse: %v", err)
		}
	}
}

// BenchmarkFingerprint exercises the fingerprint path on its own so
// regressions in pg_query's fingerprinter surface without conflating
// parse cost.
func BenchmarkFingerprint(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := pg.Fingerprint(benchSQL); err != nil {
			b.Fatalf("fingerprint: %v", err)
		}
	}
}

// BenchmarkDeparse measures the deparse path used by the rewriter after
// AST mutation. Tree is cached outside the loop.
func BenchmarkDeparse(b *testing.B) {
	tree, err := pg.Parse(benchSQL)
	if err != nil {
		b.Fatalf("setup parse: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := pg.Deparse(tree); err != nil {
			b.Fatalf("deparse: %v", err)
		}
	}
}
