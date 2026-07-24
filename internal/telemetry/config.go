// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"io"
	"log/slog"
	"os"
)

// Config bundles everything Init needs: structured logging, Prometheus
// metrics, and OpenTelemetry tracing.
type Config struct {
	Service ServiceInfo
	Logging LoggingConfig
	Metrics MetricsConfig
	Tracing TracingConfig
}

// ServiceInfo is copied into slog attributes and the sluice_build_info gauge.
type ServiceInfo struct {
	Name        string
	Version     string
	Commit      string
	Instance    string
	Environment string
}

// LoggingConfig configures the slog default handler.
type LoggingConfig struct {
	Level  slog.Level
	Format string // "json" (default) or "text"
	Output io.Writer
}

// MetricsConfig toggles the Prometheus registry wiring. The HTTP handler is
// exposed separately (MetricsHandler) so the transport layer owns ListenAndServe.
type MetricsConfig struct {
	Enabled bool
}

// TracingConfig mirrors the server config's tracing block (telemetry does
// not import internal/config, matching Logging/Metrics).
type TracingConfig struct {
	Enabled     bool
	Endpoint    string // OTLP host:port
	Protocol    string // "grpc" (default) or "http"
	Insecure    bool
	SampleRatio float64 // 0..1, parent-based
}

// DefaultConfig returns a production-default config: JSON logs at info level,
// metrics enabled.
func DefaultConfig(svc ServiceInfo) Config {
	return Config{
		Service: svc,
		Logging: LoggingConfig{
			Level:  slog.LevelInfo,
			Format: "json",
			Output: os.Stderr,
		},
		Metrics: MetricsConfig{Enabled: true},
	}
}
