// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ratelimit provides per-subject token-bucket rate limiting. The
// per-subject rate/burst comes from SubjectBinding.spec.rateLimit; each
// distinct subject gets its own bucket so one caller cannot starve another.
// The limiter is safe for concurrent use and its specs are hot-reloadable.
package ratelimit

import (
	"sync"
	"time"
)

// Spec is a subject's configured rate limit. A zero RPS means "no limit".
type Spec struct {
	RPS   float64
	Burst int
}

// normalized gives an active spec a usable burst: an unset burst defaults
// to one second's worth of tokens (at least 1).
func (s Spec) normalized() Spec {
	if s.RPS > 0 && s.Burst <= 0 {
		s.Burst = max(1, int(s.RPS))
	}
	return s
}

// Limiter holds a token bucket per subject key. Specs are resolved by
// subject id first, then by issuer, so an API key with a fixed subject and
// a JWT issuer that applies a shared per-user rate both work.
type Limiter struct {
	clock func() time.Time

	mu        sync.Mutex
	def       Spec
	bySubject map[string]Spec
	byIssuer  map[string]Spec
	buckets   map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
	spec   Spec
}

// take refills the bucket for the elapsed time (capped at burst) and
// consumes one token when available. Callers hold the owning lock.
func (b *bucket) take(now time.Time, spec Spec) bool {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * spec.RPS
		maxTokens := float64(spec.Burst)
		if b.tokens > maxTokens {
			b.tokens = maxTokens
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// New returns an empty Limiter that permits everything until SetSpecs runs.
func New(clock func() time.Time) *Limiter {
	if clock == nil {
		clock = time.Now
	}
	return &Limiter{
		clock:     clock,
		bySubject: map[string]Spec{},
		byIssuer:  map[string]Spec{},
		buckets:   map[string]*bucket{},
	}
}

// SetSpecs atomically replaces the per-subject and per-issuer rate specs.
// Existing buckets whose spec changed are dropped so the new limit applies
// on the next request. Called on config reload.
func (l *Limiter) SetSpecs(bySubject, byIssuer map[string]Spec) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bySubject = bySubject
	l.byIssuer = byIssuer
	l.buckets = map[string]*bucket{}
}

// SetDefault sets the fallback spec applied to subjects whose binding
// carries no explicit rate limit. Set once at boot from server config;
// SetSpecs (config reload) leaves it untouched. A zero-RPS spec disables
// the fallback.
func (l *Limiter) SetDefault(spec Spec) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.def = spec.normalized()
	l.buckets = map[string]*bucket{}
}

// specFor resolves the effective spec for (subject, issuer): subject
// binding first, then issuer binding, then the configured default. Returns
// a zero Spec (no limit) when none applies.
func (l *Limiter) specFor(subject, issuer string) (Spec, bool) {
	if s, ok := l.bySubject[subject]; ok && s.RPS > 0 {
		return s, true
	}
	if s, ok := l.byIssuer[issuer]; ok && s.RPS > 0 {
		return s, true
	}
	if l.def.RPS > 0 {
		return l.def, true
	}
	return Spec{}, false
}

// Allow reports whether a request from (subject, issuer) is within its rate
// limit, consuming one token when it is. Subjects with no configured limit
// (binding, issuer, or default) are always allowed. An empty subject is
// treated as unlimited here — anonymous throughput is bounded by the
// transport-level global/per-IP buckets and the concurrency gate instead.
func (l *Limiter) Allow(subject, issuer string) bool {
	if subject == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	spec, ok := l.specFor(subject, issuer)
	if !ok {
		return true
	}
	now := l.clock()
	b := l.buckets[subject]
	if b == nil || b.spec != spec {
		b = &bucket{tokens: float64(spec.Burst), last: now, spec: spec}
		l.buckets[subject] = b
	}
	return b.take(now, spec)
}
