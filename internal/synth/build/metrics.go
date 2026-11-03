// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

var (
	metricsOnce   sync.Once
	buildDuration *prometheus.HistogramVec
	buildsTotal   *prometheus.CounterVec
	staleGauge    *prometheus.GaugeVec
	spikesGauge   *prometheus.GaugeVec
	// metricsEnabled is set after the handles, so a build goroutine that
	// sees it also sees them.
	metricsEnabled atomic.Bool
)

// RegisterMetrics registers the synthetic build metrics in the current
// telemetry registry. Only the first call registers, because cmd/sluice
// tests build the runtime many times in one process.
func RegisterMetrics() {
	metricsOnce.Do(func() {
		buildDuration = telemetry.DefineHistogram(
			"sluice_synthetic_build_duration_seconds",
			"Duration of synthetic builds in the server.",
			nil,
			[]float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
		)
		buildsTotal = telemetry.DefineCounter(
			"sluice_synthetic_builds_total",
			"Finished synthetic builds in the server.",
			[]string{"result"},
		)
		staleGauge = telemetry.DefineGauge(
			"sluice_synthetic_stale",
			"1 when the current snapshot was built from another spec.",
			[]string{"name"},
		)
		spikesGauge = telemetry.DefineGauge(
			"sluice_synthetic_spikes",
			"Spikes in the report of the current snapshot.",
			[]string{"name"},
		)
		metricsEnabled.Store(true)
	})
}

// The helpers are no-ops until RegisterMetrics ran.

func observeBuild(result string, d time.Duration) {
	if !metricsEnabled.Load() {
		return
	}
	buildDuration.WithLabelValues().Observe(d.Seconds())
	buildsTotal.WithLabelValues(result).Inc()
}

func setStale(name string, stale bool) {
	if !metricsEnabled.Load() {
		return
	}
	v := 0.0
	if stale {
		v = 1
	}
	staleGauge.WithLabelValues(name).Set(v)
}

func setSpikes(name string, n int) {
	if !metricsEnabled.Load() {
		return
	}
	spikesGauge.WithLabelValues(name).Set(float64(n))
}
