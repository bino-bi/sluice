// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInit_RegistersBuildInfo(t *testing.T) {
	withIsolatedRegistry(t)

	cfg := DefaultConfig(ServiceInfo{
		Name:        "sluice",
		Version:     "0.1.0",
		Commit:      "abc1234",
		Instance:    "host-1",
		Environment: "test",
	})
	cfg.Logging.Output = io.Discard

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "sluice_build_info{") {
		t.Fatalf("sluice_build_info missing from /metrics:\n%s", body)
	}
	if !strings.Contains(body, `version="0.1.0"`) {
		t.Fatalf("version label missing:\n%s", body)
	}
	if !strings.Contains(body, `environment="test"`) {
		t.Fatalf("environment label missing:\n%s", body)
	}
}

func TestInit_IdempotentAcrossCalls(t *testing.T) {
	withIsolatedRegistry(t)

	cfg := DefaultConfig(ServiceInfo{Version: "0.1.0"})
	cfg.Logging.Output = io.Discard

	if _, err := Init(context.Background(), cfg); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// Second Init must not panic on duplicate build_info registration.
	if _, err := Init(context.Background(), cfg); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestInit_MetricsDisabled(t *testing.T) {
	withIsolatedRegistry(t)

	cfg := DefaultConfig(ServiceInfo{Version: "0.1.0"})
	cfg.Logging.Output = io.Discard
	cfg.Metrics.Enabled = false

	if _, err := Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "sluice_build_info") {
		t.Fatalf("build_info should not be registered when Metrics.Enabled=false")
	}
}
