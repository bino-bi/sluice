// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

// metricsSet is the bundle of Prometheus series owned by the audit
// dispatcher. Registration is lazy so tests that swap the global registry
// via telemetry.SetRegistry can do so before metrics are declared.
type metricsSet struct {
	QueueDepth     *prometheus.GaugeVec
	DroppedTotal   *prometheus.CounterVec
	WriteErrors    *prometheus.CounterVec
	RotateDuration *prometheus.HistogramVec
	Enqueued       *prometheus.CounterVec
}

var (
	metricsOnce   sync.Once
	metricsCached *metricsSet
)

// metricsShared returns the package-wide metric set, registering it with
// the current telemetry registry on first use.
func metricsShared() *metricsSet {
	metricsOnce.Do(func() {
		metricsCached = &metricsSet{
			QueueDepth:     telemetry.DefineGauge("sluice_audit_queue_depth", "Current audit dispatcher queue depth.", []string{"sink"}),
			DroppedTotal:   telemetry.DefineCounter("sluice_audit_dropped_total", "Audit records dropped before reaching a sink.", []string{"sink", "reason"}),
			WriteErrors:    telemetry.DefineCounter("sluice_audit_write_errors_total", "Audit sink write errors.", []string{"sink"}),
			RotateDuration: telemetry.DefineHistogram("sluice_audit_rotate_duration_seconds", "Wall-clock time spent rotating an audit file.", []string{"sink"}, nil),
			Enqueued:       telemetry.DefineCounter("sluice_audit_enqueued_total", "Audit records accepted into the dispatcher queue.", []string{"event_type"}),
		}
	})
	return metricsCached
}
