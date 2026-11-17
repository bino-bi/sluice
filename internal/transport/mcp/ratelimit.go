// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
)

var (
	rateLimitedOnce    sync.Once
	rateLimitedCounter *prometheus.CounterVec
)

// rateLimitedMetric counts the refusals. As on the REST transport they are
// metered and not audited: there is no subject yet, and a record per
// refused request would turn a flood against the audit pipeline.
func rateLimitedMetric() *prometheus.CounterVec {
	rateLimitedOnce.Do(func() {
		rateLimitedCounter = telemetry.DefineCounter(
			"sluice_mcp_rate_limited_total",
			"MCP requests over HTTP refused by the transport-level rate limiter, before identity resolution.",
			[]string{"scope"})
	})
	return rateLimitedCounter
}

func (s *Server) denyRateLimited(w http.ResponseWriter, r *http.Request, scope string) {
	rateLimitedMetric().WithLabelValues(scope).Inc()
	s.lg.DebugContext(r.Context(), "mcp: request rate limited",
		"scope", scope,
		"remote", r.RemoteAddr,
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"code":"ERR_RATE_LIMITED","message":"request rate limit exceeded"}`))
}

// writeInternalError is the response to a recovered panic. It never
// leaks detail.
func writeInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"code":"ERR_INTERNAL","message":"internal error"}`))
}
