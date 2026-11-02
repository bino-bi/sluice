// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

var (
	sampleRequestsOnce    sync.Once
	sampleRequestsCounter *prometheus.CounterVec
)

// sampleRequestsMetric counts sample requests that passed validation.
func sampleRequestsMetric() *prometheus.CounterVec {
	sampleRequestsOnce.Do(func() {
		sampleRequestsCounter = telemetry.DefineCounter(
			"sluice_sample_requests_total",
			"Sample requests that passed validation, by method.",
			[]string{"method"})
	})
	return sampleRequestsCounter
}
