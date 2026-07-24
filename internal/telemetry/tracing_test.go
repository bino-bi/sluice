// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/bino-bi/sluice/internal/telemetry"
)

// Global otel state: these tests must not run in parallel and restore
// the provider on cleanup.
func snapshotGlobals(t *testing.T) {
	t.Helper()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
}

func TestInit_TracingDisabledLeavesNoopProvider(t *testing.T) {
	snapshotGlobals(t)
	before := otel.GetTracerProvider()

	cfg := telemetry.DefaultConfig(telemetry.ServiceInfo{Name: "sluice-test"})
	cfg.Metrics.Enabled = false
	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if otel.GetTracerProvider() != before {
		t.Fatal("disabled tracing must not install a provider")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown: %v", err)
	}
}

func TestInit_TracingEnabledInstallsSDKProvider(t *testing.T) {
	snapshotGlobals(t)

	cfg := telemetry.DefaultConfig(telemetry.ServiceInfo{Name: "sluice-test", Version: "test"})
	cfg.Metrics.Enabled = false
	cfg.Tracing = telemetry.TracingConfig{
		Enabled:     true,
		Endpoint:    "127.0.0.1:14317", // nothing listens; the batching exporter must not block
		Protocol:    "grpc",
		Insecure:    true,
		SampleRatio: 1.0,
	}
	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("provider = %T; want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}
	// Shutdown with a short deadline: it flushes to a dead endpoint and
	// must return (with or without error), not hang.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestInit_TracingUnknownProtocolErrors(t *testing.T) {
	snapshotGlobals(t)
	cfg := telemetry.DefaultConfig(telemetry.ServiceInfo{Name: "sluice-test"})
	cfg.Metrics.Enabled = false
	cfg.Tracing = telemetry.TracingConfig{Enabled: true, Endpoint: "x:1", Protocol: "udp"}
	if _, err := telemetry.Init(context.Background(), cfg); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}
