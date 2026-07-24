// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/rest"
)

func TestTracing_ServerSpanPerRequest(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})

	deps := rest.Deps{Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}}}

	// Tracing on: one server span, named from the mux pattern.
	traced := rest.New(rest.Config{Listen: ":0", Tracing: true}, deps)
	w := httptest.NewRecorder()
	traced.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d; want 1", len(spans))
	}
	if got := spans[0].Name; got != "GET /v1/health" {
		t.Fatalf("span name = %q; want GET /v1/health", got)
	}

	// Tracing off: no wrapping, no spans.
	exp.Reset()
	plain := rest.New(rest.Config{Listen: ":0", Tracing: false}, deps)
	w = httptest.NewRecorder()
	plain.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if n := len(exp.GetSpans()); n != 0 {
		t.Fatalf("spans = %d; want 0 with tracing disabled", n)
	}
}
