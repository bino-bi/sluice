// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"io"
	"log/slog"
	"os"
)

// Config bundles everything Init needs. Only the MVP subset is populated in
// this slice: structured logging and Prometheus metrics. Tracing and metrics
// endpoint wiring land with the query-path transport slice.
type Config struct {
	Service ServiceInfo
	Logging LoggingConfig
	Metrics MetricsConfig
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
