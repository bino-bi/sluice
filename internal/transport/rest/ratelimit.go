// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
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

// rateLimitedMetric counts transport-level refusals. Denials happen before
// identity resolution, so they are metered and debug-logged rather than
// audited — there is no subject to attribute, and auditing a flood would
// amplify it against a fail-closed audit pipeline.
func rateLimitedMetric() *prometheus.CounterVec {
	rateLimitedOnce.Do(func() {
		rateLimitedCounter = telemetry.DefineCounter(
			"sluice_rest_rate_limited_total",
			"REST requests refused by the transport-level rate limiter, before identity resolution.",
			[]string{"scope"})
	})
	return rateLimitedCounter
}

// rateLimitMW enforces the global and per-IP token buckets ahead of the
// identity middleware on the data-plane route. Either limiter may be nil.
func (s *Server) rateLimitMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.GlobalLimiter != nil && !s.deps.GlobalLimiter.Allow("") {
			s.denyRateLimited(w, r, "global")
			return
		}
		if s.deps.PerIPLimiter != nil && !s.deps.PerIPLimiter.Allow(remoteIP(r)) {
			s.denyRateLimited(w, r, "per_ip")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) denyRateLimited(w http.ResponseWriter, r *http.Request, scope string) {
	rateLimitedMetric().WithLabelValues(scope).Inc()
	s.lg.DebugContext(r.Context(), "rest: request rate limited",
		"scope", scope,
		"remote", r.RemoteAddr,
	)
	writeError(w, r, pkgerr.New(pkgerr.CodeRateLimited).WithMessage("request rate limit exceeded"))
}

// remoteIP extracts the host from RemoteAddr, falling back to the whole
// string when it does not parse as host:port.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
