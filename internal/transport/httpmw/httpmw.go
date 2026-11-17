// SPDX-License-Identifier: AGPL-3.0-or-later

package httpmw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/telemetry"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// BodyCap limits a request body to maxBytes. A handler that reads more
// gets an *http.MaxBytesError.
func BodyCap(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.ContentLength != 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Timeout puts a deadline of d on the request context. It does not use
// http.TimeoutHandler, so a streaming handler can still flush what it
// has when the deadline passes.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type requestIDKey struct{}

// RequestID takes the request id from header, or makes a new one, echoes
// it in the response header and puts it on the request context.
func RequestID(header string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := r.Header.Get(header)
			if rid == "" {
				rid = newRequestID()
			}
			w.Header().Set(header, rid)
			ctx := context.WithValue(r.Context(), requestIDKey{}, rid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext returns the id that RequestID put on ctx.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var (
	panicsOnce    sync.Once
	panicsCounter *prometheus.CounterVec
)

func panicsMetric() *prometheus.CounterVec {
	panicsOnce.Do(func() {
		panicsCounter = telemetry.DefineCounter(
			"sluice_http_panics_total",
			"HTTP handler panics that the server recovered from.",
			[]string{"transport"})
	})
	return panicsCounter
}

// Recover turns a handler panic into an error log, a count in
// sluice_http_panics_total and the response that write sends. The stack
// goes to the log only. http.ErrAbortHandler is panicked again: a handler
// uses it to abort a response that has already started.
func Recover(transport string, log *slog.Logger, write func(http.ResponseWriter)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				panicsMetric().WithLabelValues(transport).Inc()
				log.ErrorContext(r.Context(), transport+": panic recovered",
					"path", r.URL.Path,
					"recover", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
				)
				write(w)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Limiter is a token bucket per key (ratelimit.KeyedLimiter).
type Limiter interface {
	Allow(key string) bool
}

// RateLimit applies the per-IP bucket, then the global bucket, before
// the handler. It belongs in front of the login, so a flood costs no JWKS
// fetch and no key hash. A nil limiter is no limit. deny writes the
// refusal; scope is "per_ip" or "global".
func RateLimit(perIP, global Limiter, deny func(w http.ResponseWriter, r *http.Request, scope string)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Per IP first: a request this bucket refuses must not take a
			// token of the global one, or one client starves all others.
			if perIP != nil && !perIP.Allow(identity.RemoteIP(r)) {
				deny(w, r, "per_ip")
				return
			}
			if global != nil && !global.Allow("") {
				deny(w, r, "global")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
