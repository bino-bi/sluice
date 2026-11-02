// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func reservoirSpec(rows int64) *rewriter.SampleSpec {
	return &rewriter.SampleSpec{Method: apitypes.SampleMethodReservoir, Rows: rows, Seed: 7}
}

func TestRewrite_SampleForcesMutation(t *testing.T) {
	rw := buildRewriter(t)
	sql := `SELECT "id" FROM "pg"."public"."orders"`
	dec, ast := evaluate(t, sql, nil, allowAll(0))

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql, Sample: reservoirSpec(3),
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !res.Changed || !res.Sampled {
		t.Errorf("Changed = %v, Sampled = %v, want both true", res.Changed, res.Sampled)
	}
	want := "SELECT * FROM (SELECT id FROM pg.public.orders) AS sluice_endpoint_sample USING SAMPLE 3 ROWS (reservoir, 7)"
	if res.SQL != want {
		t.Errorf("SQL = %q, want %q", res.SQL, want)
	}
}

func TestRewrite_SampleRefusesOtherShape(t *testing.T) {
	rw := buildRewriter(t)
	sql := `SELECT "id" FROM "pg"."public"."orders" WHERE (1=1) UNION SELECT id FROM pg.public.orders WHERE (1=1)`
	dec, ast := evaluate(t, sql, nil, allowAll(0))

	_, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql, Sample: reservoirSpec(3),
	})
	if err == nil || !strings.Contains(err.Error(), "where must be one predicate") {
		t.Fatalf("err = %v, want shape refusal", err)
	}
}

func TestRewrite_SampleUnderPolicySample(t *testing.T) {
	rw := buildRewriter(t)
	sql := `SELECT "id" FROM "pg"."public"."orders"`
	_, ast := evaluate(t, sql, nil, allowAll(0))
	dec := &policy.Decision{
		Outcome: policy.OutcomeAllow,
		Rewrite: &policy.RewriteEffect{Sample: &policy.CompiledSample{Rate: 0.5, Method: apitypes.SampleBernoulli}},
	}

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql, Sample: reservoirSpec(3),
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	want := "SELECT * FROM (SELECT * FROM (SELECT id FROM pg.public.orders) AS sluice_endpoint_sample USING SAMPLE 3 ROWS (reservoir, 7)) AS sluice_sample USING SAMPLE 50% (bernoulli)"
	if res.SQL != want {
		t.Errorf("SQL = %q, want %q", res.SQL, want)
	}
	if !slices.Equal(res.Rewrites, []string{"sample-endpoint:reservoir:3", "sample:bernoulli:50%"}) {
		t.Errorf("Rewrites = %v", res.Rewrites)
	}
}

func TestRewrite_SampleDefersPolicyLimit(t *testing.T) {
	rw := buildRewriter(t)
	sql := `SELECT "id" FROM "pg"."public"."orders"`
	dec, ast := evaluate(t, sql, nil, allowAll(0), capRows(3))

	res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
		AST: ast, Decision: dec, Raw: sql, Sample: reservoirSpec(20),
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if strings.Contains(res.SQL, "LIMIT") {
		t.Errorf("SQL = %q, want no LIMIT", res.SQL)
	}
	if !slices.Contains(res.Rewrites, "limit-deferred:sample") {
		t.Errorf("Rewrites = %v, want limit-deferred:sample", res.Rewrites)
	}
}
