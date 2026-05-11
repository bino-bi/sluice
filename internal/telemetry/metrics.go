// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// defaultRegisterer is the Prometheus registry Init writes into. Tests may
// replace it via SetRegistry; the default points at prometheus.DefaultRegisterer
// so the standard /metrics handler keeps working.
var (
	defaultRegisterer prometheus.Registerer = prometheus.DefaultRegisterer
	defaultGatherer   prometheus.Gatherer   = prometheus.DefaultGatherer
)

// SetRegistry swaps the registerer/gatherer pair. Intended for tests — the
// production binary keeps the global default.
func SetRegistry(reg *prometheus.Registry) {
	defaultRegisterer = reg
	defaultGatherer = reg
}

// ResetRegistryToDefault restores the process-wide Prometheus default. Used
// by tests to avoid leaking state across packages.
func ResetRegistryToDefault() {
	defaultRegisterer = prometheus.DefaultRegisterer
	defaultGatherer = prometheus.DefaultGatherer
}

// DefineCounter registers a CounterVec. Panics on duplicate registration so
// every metric in the catalog is declared exactly once, ideally in package
// init() of its owning package.
func DefineCounter(name, help string, labels []string) *prometheus.CounterVec {
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	defaultRegisterer.MustRegister(cv)
	return cv
}

// DefineHistogram registers a HistogramVec. buckets=nil uses DefaultBuckets().
func DefineHistogram(name, help string, labels []string, buckets []float64) *prometheus.HistogramVec {
	if buckets == nil {
		buckets = DefaultBuckets()
	}
	hv := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    name,
		Help:    help,
		Buckets: buckets,
	}, labels)
	defaultRegisterer.MustRegister(hv)
	return hv
}

// DefineGauge registers a GaugeVec. Use labels=nil for a single-value gauge.
func DefineGauge(name, help string, labels []string) *prometheus.GaugeVec {
	gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	defaultRegisterer.MustRegister(gv)
	return gv
}

// DefaultBuckets are the seconds-scale SLO buckets used for every duration
// histogram in sluice unless the owning package overrides.
func DefaultBuckets() []float64 {
	return []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
}

// MetricsHandler returns the http.Handler that serves the Prometheus text
// exposition format from the current registry. The transport layer decides
// where to mount it.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(defaultGatherer, promhttp.HandlerOpts{})
}
