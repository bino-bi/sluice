// SPDX-License-Identifier: AGPL-3.0-or-later

package schema

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

// metrics owns the Prometheus handles consumed by the cache. Fields are
// registered once per process via initMetrics.
type metrics struct {
	hits    *prometheus.CounterVec
	misses  *prometheus.CounterVec
	refresh prometheus.Histogram
	stales  *prometheus.CounterVec
}

// globalMetrics is the package-singleton handed to every Cache. The cache
// is tiny and there is no reason for multiple independent instances to
// report separately — they all share the same counter set.
var (
	globalMetrics *metrics
	metricsOnce   sync.Once
)

func init() {
	// Registered lazily via initMetrics so tests that call
	// telemetry.SetRegistry before constructing a Cache can override the
	// destination. init() only allocates the struct.
	globalMetrics = &metrics{}
}

// initMetrics lazily registers Prometheus metrics into the current
// telemetry registry. Subsequent calls are no-ops so test registries do
// not produce duplicate registrations.
func initMetrics() {
	metricsOnce.Do(func() {
		globalMetrics.hits = telemetry.DefineCounter(
			"sluice_schema_cache_hits_total",
			"Schema cache hits.",
			[]string{"catalog"},
		)
		globalMetrics.misses = telemetry.DefineCounter(
			"sluice_schema_cache_misses_total",
			"Schema cache misses that triggered an introspection.",
			[]string{"catalog"},
		)
		globalMetrics.refresh = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sluice_schema_refresh_duration_seconds",
			Help:    "Duration of Refresh calls.",
			Buckets: telemetry.DefaultBuckets(),
		})
		// Non-vector histogram registration uses the package's default
		// registerer via DefineHistogram's underlying path; for a single
		// series we manually wire it so the histogram type is not a vec.
		// We re-use DefineCounter's registration mechanics for stales.
		// refresh is registered into DefaultRegisterer separately:
		prometheus.DefaultRegisterer.MustRegister(globalMetrics.refresh)
		globalMetrics.stales = telemetry.DefineCounter(
			"sluice_schema_stale_entries",
			"Number of times a cached entry became stale after a refresh failure.",
			[]string{"catalog"},
		)
	})
}

// hit increments the hit counter. When metrics have not been registered
// yet (tests that skip initMetrics) the call is a no-op.
func (m *metrics) hit(catalog string) {
	if m == nil || m.hits == nil {
		return
	}
	m.hits.WithLabelValues(catalog).Inc()
}

// miss increments the miss counter.
func (m *metrics) miss(catalog string) {
	if m == nil || m.misses == nil {
		return
	}
	m.misses.WithLabelValues(catalog).Inc()
}

// observeRefresh records a refresh duration in seconds.
func (m *metrics) observeRefresh(secs float64) {
	if m == nil || m.refresh == nil {
		return
	}
	m.refresh.Observe(secs)
}

// stale increments the stale counter.
func (m *metrics) stale(catalog string) {
	if m == nil || m.stales == nil {
		return
	}
	m.stales.WithLabelValues(catalog).Inc()
}

// RegisterMetrics wires the schema cache counters into the current
// telemetry registry. Call once at composition time (cmd/sluice). Safe
// to call multiple times; only the first effects registration.
func RegisterMetrics() { initMetrics() }
