// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

// metrics owns the Prometheus handles consumed by the engine. Registered
// lazily via RegisterMetrics so tests can swap the telemetry registry
// before the first Evaluate call.
type metrics struct {
	evaluations *prometheus.CounterVec
	denials     *prometheus.CounterVec
	rejections  *prometheus.CounterVec
	duration    prometheus.Histogram
}

var (
	globalMetrics = &metrics{}
	metricsOnce   sync.Once
)

// RegisterMetrics registers the policy engine counters into the current
// telemetry registry. Safe to call more than once; only the first effects
// registration.
func RegisterMetrics() {
	metricsOnce.Do(func() {
		globalMetrics.evaluations = telemetry.DefineCounter(
			"sluice_policy_evaluations_total",
			"Policy engine evaluations by outcome.",
			[]string{"outcome"},
		)
		globalMetrics.denials = telemetry.DefineCounter(
			"sluice_policy_denials_total",
			"Policy engine denials by policy name.",
			[]string{"policy"},
		)
		globalMetrics.rejections = telemetry.DefineCounter(
			"sluice_policy_rejections_total",
			"Policy engine rejections by policy + rule.",
			[]string{"policy", "rule"},
		)
		globalMetrics.duration = prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "sluice_policy_eval_duration_seconds",
			Help:    "Policy engine evaluation duration.",
			Buckets: telemetry.DefaultBuckets(),
		})
		prometheus.DefaultRegisterer.MustRegister(globalMetrics.duration)
	})
}

func (m *metrics) evaluated(outcome Outcome) {
	if m == nil || m.evaluations == nil {
		return
	}
	m.evaluations.WithLabelValues(string(outcome)).Inc()
}

func (m *metrics) denied(policy string) {
	if m == nil || m.denials == nil {
		return
	}
	m.denials.WithLabelValues(policy).Inc()
}

func (m *metrics) rejected(policy, rule string) {
	if m == nil || m.rejections == nil {
		return
	}
	m.rejections.WithLabelValues(policy, rule).Inc()
}

func (m *metrics) observe(secs float64) {
	if m == nil || m.duration == nil {
		return
	}
	m.duration.Observe(secs)
}
