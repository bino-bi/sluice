// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/version"
)

// Init configures the slog default logger, the sluice_build_info gauge
// (when metrics are enabled), and the OTel tracer provider (when tracing
// is enabled). The returned shutdown flushes pending spans; callers must
// defer it.
func Init(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	slog.SetDefault(slog.New(newSlogHandler(cfg.Logging)))

	if cfg.Metrics.Enabled {
		if err := registerBuildInfo(cfg.Service); err != nil {
			return noopShutdown, err
		}
	}

	if cfg.Tracing.Enabled {
		return initTracing(ctx, cfg.Service, cfg.Tracing)
	}
	return noopShutdown, nil
}

func registerBuildInfo(svc ServiceInfo) error {
	gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sluice_build_info",
		Help: "Build identity of the running sluice binary. Value is always 1.",
	}, []string{"version", "commit", "go", "platform", "parser", "instance", "environment"})

	if err := defaultRegisterer.Register(gv); err != nil {
		// If another Init already registered the gauge, reuse it so tests
		// that call Init repeatedly don't panic.
		var are prometheus.AlreadyRegisteredError
		if !errors.As(err, &are) {
			return err
		}
		existing, ok := are.ExistingCollector.(*prometheus.GaugeVec)
		if !ok {
			return err
		}
		gv = existing
	}

	gv.With(prometheus.Labels{
		"version":     svc.Version,
		"commit":      svc.Commit,
		"go":          runtime.Version(),
		"platform":    runtime.GOOS + "/" + runtime.GOARCH,
		"parser":      version.Current().Parser,
		"instance":    svc.Instance,
		"environment": svc.Environment,
	}).Set(1)
	return nil
}

func noopShutdown(context.Context) error { return nil }
