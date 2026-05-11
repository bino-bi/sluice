// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// BenchmarkRewriteRowFilter is the hot-path benchmark: one row filter with
// a subject-template + one positional parameter. Regression target ties
// into plan 24 §10's >5% regression gate.
func BenchmarkRewriteRowFilter(b *testing.B) {
	p := pgquery.New(parser.Options{})
	ctx := context.Background()
	sql := "SELECT id, total FROM pg.public.orders WHERE active = true"

	allow := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "allow-all"},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectAllow,
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}},
			}},
		},
	}
	rf := &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: "tenant"},
		Spec: apitypes.RowFilterSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}}},
			}},
			Filter: apitypes.FilterSpec{
				Predicate: &apitypes.Predicate{
					Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "{{ subject.tenant_id }}",
				},
			},
		},
	}

	eng := policy.New(policy.Options{})
	src := &config.Snapshot{
		Policies: []apitypes.Object{allow, rf},
		ByKind: map[apitypes.Kind][]apitypes.Object{
			apitypes.KindSQLAccessPolicy: {allow},
			apitypes.KindRowFilterPolicy: {rf},
		},
	}
	if err := eng.ApplySnapshot(ctx, src); err != nil {
		b.Fatalf("apply snapshot: %v", err)
	}

	ast, err := p.Parse(ctx, sql)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	user := &identity.UserCtx{Subject: "bench", Claims: map[string]any{"tenant_id": "t-1"}}
	dec, err := eng.Evaluate(ctx, policy.Input{
		User:   user,
		AST:    ast,
		Shape:  ast.Shape(),
		Tables: ast.Tables(),
	})
	if err != nil {
		b.Fatalf("evaluate: %v", err)
	}

	rw := rewriter.New(rewriter.Options{Parser: p, DefaultCatalog: "pg"})
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := rw.Rewrite(ctx, rewriter.RewriteRequest{
			AST:      ast,
			Decision: dec,
			User:     user,
			Raw:      sql,
		}); err != nil {
			b.Fatalf("rewrite: %v", err)
		}
	}
}
