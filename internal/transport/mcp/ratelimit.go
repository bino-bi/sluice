// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/telemetry"
)

// keyLimiter is the transport-level bucket contract (ratelimit.KeyedLimiter
// in production). Nil disables the respective bucket.
type keyLimiter interface {
	Allow(key string) bool
}

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

// rateLimitMW enforces the global and per-IP token buckets on every
// Streamable HTTP request, ahead of authMiddleware. Either limiter may be
// nil.
func (s *Server) rateLimitMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := ""
		switch {
		case s.deps.GlobalLimiter != nil && !s.deps.GlobalLimiter.Allow(""):
			scope = "global"
		case s.deps.PerIPLimiter != nil && !s.deps.PerIPLimiter.Allow(identity.RemoteIP(r)):
			scope = "per_ip"
		}
		if scope == "" {
			next.ServeHTTP(w, r)
			return
		}
		rateLimitedMetric().WithLabelValues(scope).Inc()
		s.lg.DebugContext(r.Context(), "mcp: request rate limited",
			"scope", scope,
			"remote", r.RemoteAddr,
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"ERR_RATE_LIMITED","message":"request rate limit exceeded"}`))
	})
}
