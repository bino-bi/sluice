// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

var (
	metricsOnce sync.Once
	mPending    prometheus.Gauge
	mDecisions  *prometheus.CounterVec
	mWaitSecs   prometheus.Observer
	mWebhook    *prometheus.CounterVec
)

// registerMetrics lazily registers the approval metrics so tests can swap
// the Prometheus registry first.
func registerMetrics() {
	metricsOnce.Do(func() {
		mPending = telemetry.DefineGauge("sluice_approval_pending",
			"Approval requests currently awaiting a decision.", nil).WithLabelValues()
		mDecisions = telemetry.DefineCounter("sluice_approval_decisions_total",
			"Approval decisions by outcome.", []string{"outcome"})
		mWaitSecs = telemetry.DefineHistogram("sluice_approval_wait_seconds",
			"Time a request waited before being decided or timing out.",
			nil, telemetry.DefaultBuckets()).WithLabelValues()
		mWebhook = telemetry.DefineCounter("sluice_approval_webhook_deliveries_total",
			"Approval webhook delivery attempts by status.", []string{"status"})
	})
}
