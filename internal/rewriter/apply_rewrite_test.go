// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func capRows(maxRows int64) *apitypes.QueryRewritePolicy {
	return &apitypes.QueryRewritePolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindQueryRewritePolicy},
		Metadata: apitypes.ObjectMeta{Name: "cap-rows", Priority: 50},
		Spec: apitypes.QueryRewriteSpec{
			Match: apitypes.Selector{
				Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}},
			},
			Rewrite: apitypes.RewriteSpec{Limit: &apitypes.LimitSpec{Max: maxRows}},
		},
	}
}

func TestRewriter_RewriteLimitKeepsSmallerExisting(t *testing.T) {
	rw := buildRewriter(t)
	sql := "SELECT id FROM pg.public.orders LIMIT 10"
	dec, ast := evaluate(t, sql, nil, allowAll(0), capRows(1000))

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "LIMIT 10") {
		t.Errorf("SQL = %q, want existing LIMIT 10 kept", res.SQL)
	}
	for _, note := range res.Rewrites {
		if strings.HasPrefix(note, "limit-") {
			t.Errorf("unexpected limit annotation %q for an already-compliant query", note)
		}
	}
}

func TestRewriter_RewriteLimitSkipsNonSelect(t *testing.T) {
	rw := buildRewriter(t)
	p := pgquery.New(parser.Options{})
	sql := "EXPLAIN SELECT id FROM pg.public.orders"
	ast, err := p.Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec := &policy.Decision{
		Outcome: policy.OutcomeAllow,
		Rewrite: &policy.RewriteEffect{LimitMax: 5},
	}
	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(res.SQL, "LIMIT") {
		t.Errorf("SQL = %q, want no LIMIT on EXPLAIN", res.SQL)
	}
	if !slices.Contains(res.Rewrites, "limit-skipped:not-select") {
		t.Errorf("Rewrites = %v, want limit-skipped:not-select", res.Rewrites)
	}
}

func TestRewriter_SampleWrapSkipsNonSelect(t *testing.T) {
	rw := buildRewriter(t)
	p := pgquery.New(parser.Options{})
	sql := "EXPLAIN SELECT id FROM pg.public.orders"
	ast, err := p.Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec := &policy.Decision{
		Outcome: policy.OutcomeAllow,
		Rewrite: &policy.RewriteEffect{Sample: &policy.CompiledSample{Rate: 0.5, Method: apitypes.SampleBernoulli}},
	}
	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql,
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(res.SQL, "USING SAMPLE") {
		t.Errorf("SQL = %q, want no sample wrap on EXPLAIN", res.SQL)
	}
	if !slices.Contains(res.Rewrites, "sample-skipped:EXPLAIN") {
		t.Errorf("Rewrites = %v, want sample-skipped:EXPLAIN", res.Rewrites)
	}
}
