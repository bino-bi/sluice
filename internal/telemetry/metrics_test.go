// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func withIsolatedRegistry(t *testing.T) {
	t.Helper()
	reg := prometheus.NewRegistry()
	SetRegistry(reg)
	t.Cleanup(ResetRegistryToDefault)
}

func TestDefineCounter(t *testing.T) {
	withIsolatedRegistry(t)

	c := DefineCounter("sluice_test_counter", "help", []string{"outcome"})
	c.WithLabelValues("ok").Inc()
	c.WithLabelValues("ok").Inc()

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), `sluice_test_counter{outcome="ok"} 2`) {
		t.Fatalf("counter missing from /metrics output:\n%s", body)
	}
}

func TestDefineCounter_DuplicatePanics(t *testing.T) {
	withIsolatedRegistry(t)
	DefineCounter("sluice_dup", "help", nil)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	DefineCounter("sluice_dup", "help", nil)
}

func TestDefineHistogram_DefaultBuckets(t *testing.T) {
	withIsolatedRegistry(t)
	h := DefineHistogram("sluice_test_hist_seconds", "help", []string{"decision"}, nil)
	h.WithLabelValues("allow").Observe(0.042)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `sluice_test_hist_seconds_count{decision="allow"} 1`) {
		t.Fatalf("histogram count missing:\n%s", body)
	}
}

func TestDefineGauge(t *testing.T) {
	withIsolatedRegistry(t)
	g := DefineGauge("sluice_test_gauge", "help", []string{"source"})
	g.WithLabelValues("main").Set(42)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), `sluice_test_gauge{source="main"} 42`) {
		t.Fatalf("gauge value not exposed:\n%s", rec.Body.String())
	}
}

func TestDefaultBuckets(t *testing.T) {
	b := DefaultBuckets()
	if len(b) == 0 {
		t.Fatal("DefaultBuckets should not be empty")
	}
	for i := 1; i < len(b); i++ {
		if b[i] <= b[i-1] {
			t.Fatalf("buckets not strictly increasing: %v", b)
		}
	}
}
