// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/telemetry"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

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

func (s *Server) denyRateLimited(w http.ResponseWriter, r *http.Request, scope string) {
	rateLimitedMetric().WithLabelValues(scope).Inc()
	s.lg.DebugContext(r.Context(), "rest: request rate limited",
		"scope", scope,
		"remote", r.RemoteAddr,
	)
	s.writeError(w, r, pkgerr.New(pkgerr.CodeRateLimited).WithMessage("request rate limit exceeded"))
}
