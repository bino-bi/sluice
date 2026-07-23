// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// KeyedLimiter applies one Spec across an open key space — remote IPs, or a
// single constant key for a global bucket. The bucket map is LRU-bounded so
// untrusted keys cannot grow it without bound; an evicted key that returns
// starts over with a fresh burst, and the aggregate stays bounded by any
// outer global bucket.
type KeyedLimiter struct {
	clock func() time.Time
	spec  Spec

	mu      sync.Mutex
	buckets *lru.Cache[string, *bucket]
}

// NewKeyed returns a KeyedLimiter enforcing spec per key. maxKeys bounds
// the bucket map (default 10000 when <= 0). A zero-RPS spec permits
// everything.
func NewKeyed(spec Spec, maxKeys int, clock func() time.Time) *KeyedLimiter {
	if clock == nil {
		clock = time.Now
	}
	if maxKeys <= 0 {
		maxKeys = 10000
	}
	// golang-lru only rejects non-positive capacity, guarded above.
	cache, _ := lru.New[string, *bucket](maxKeys)
	return &KeyedLimiter{clock: clock, spec: spec.normalized(), buckets: cache}
}

// Allow reports whether one request under key is within the limit,
// consuming a token when it is.
func (k *KeyedLimiter) Allow(key string) bool {
	if k.spec.RPS <= 0 {
		return true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.clock()
	b, ok := k.buckets.Get(key)
	if !ok {
		b = &bucket{tokens: float64(k.spec.Burst), last: now, spec: k.spec}
		k.buckets.Add(key, b)
	}
	return b.take(now, k.spec)
}
