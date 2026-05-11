// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Cache is the minimum abstraction Resolver needs. Production uses an LRU
// with per-entry TTL; tests can inject their own.
type Cache interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl time.Duration)
	Invalidate(key string)
	InvalidateAll()
}

type lruCache struct {
	mu    sync.Mutex
	inner *lru.Cache[string, cacheEntry]
	clock func() time.Time
}

type cacheEntry struct {
	value   []byte
	expires time.Time // zero = no expiry
}

// DefaultCache returns an in-memory LRU with the given capacity and TTL. A
// zero TTL disables expiry; a zero capacity falls back to 1000.
func DefaultCache(capacity int, _ time.Duration) Cache {
	if capacity <= 0 {
		capacity = 1000
	}
	c, err := lru.New[string, cacheEntry](capacity)
	if err != nil {
		// golang-lru only rejects negative capacity, which we've already
		// filtered. Fail loud so misconfiguration is caught in dev.
		panic(err)
	}
	return &lruCache{inner: c, clock: time.Now}
}

func (c *lruCache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.inner.Get(key)
	if !ok {
		return nil, false
	}
	if !entry.expires.IsZero() && c.clock().After(entry.expires) {
		c.inner.Remove(key)
		return nil, false
	}
	// Defensive copy so callers cannot mutate the cached byte slice.
	out := make([]byte, len(entry.value))
	copy(out, entry.value)
	return out, true
}

func (c *lruCache) Set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stored := make([]byte, len(value))
	copy(stored, value)

	e := cacheEntry{value: stored}
	if ttl > 0 {
		e.expires = c.clock().Add(ttl)
	}
	c.inner.Add(key, e)
}

func (c *lruCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inner.Remove(key)
}

func (c *lruCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inner.Purge()
}
