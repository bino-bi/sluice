// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
)

// installTestTracer swaps in a synchronous in-memory exporter. Not
// parallel-safe: it mutates the otel global provider.
func installTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exp
}

func spanNames(exp *tracetest.InMemoryExporter) map[string]bool {
	out := map[string]bool{}
	for _, s := range exp.GetSpans() {
		out[s.Name] = true
	}
	return out
}

func TestExecute_EmitsStageSpansWithoutRawSQL(t *testing.T) {
	exp := installTestTracer(t)

	const rawSQL = "SELECT secret_col FROM pg.public.orders WHERE id = 42"
	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		&fakeRewriter{},
		&fakeExecutor{columns: []executor.ColumnInfo{{Name: "id"}}},
		&fakeAudit{},
	)
	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    rawSQL,
		Origin: queryservice.OriginREST,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_ = res.Rows.Close()

	names := spanNames(exp)
	for _, want := range []string{"query", "query.parse", "query.policy", "query.rewrite", "query.execute"} {
		if !names[want] {
			t.Fatalf("missing span %q; got %v", want, names)
		}
	}

	// No span attribute may carry the raw SQL text.
	for _, s := range exp.GetSpans() {
		for _, attr := range s.Attributes {
			if strings.Contains(attr.Value.String(), "secret_col") {
				t.Fatalf("span %s attribute %s leaks SQL: %s", s.Name, attr.Key, attr.Value.String())
			}
		}
	}
}

func TestExecute_DenySpanCarriesDecision(t *testing.T) {
	exp := installTestTracer(t)

	svc := buildService(t,
		&fakeParser{},
		&fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeDeny}},
		&fakeRewriter{},
		&fakeExecutor{},
		&fakeAudit{},
	)
	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    "SELECT 1",
		Origin: queryservice.OriginREST,
	})
	if err == nil {
		t.Fatal("expected deny error")
	}

	var found bool
	for _, s := range exp.GetSpans() {
		if s.Name != "query" {
			continue
		}
		for _, attr := range s.Attributes {
			if attr.Key == "sluice.query.decision" && attr.Value.AsString() == "deny" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("query span missing deny decision attribute; spans: %v", spanNames(exp))
	}
}
