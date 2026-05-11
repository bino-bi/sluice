// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCache_GetSet(t *testing.T) {
	c := DefaultCache(10, time.Minute)
	c.Set("k", []byte("v"), time.Minute)

	got, ok := c.Get("k")
	if !ok {
		t.Fatal("expected key to be present")
	}
	if string(got) != "v" {
		t.Fatalf("value = %q", got)
	}

	// Mutation of the returned slice must not affect the cached value.
	got[0] = 'x'
	got2, _ := c.Get("k")
	if string(got2) != "v" {
		t.Fatalf("cache mutated through returned slice: %q", got2)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	impl := DefaultCache(10, time.Minute).(*lruCache)

	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	impl.clock = func() time.Time { return time.Unix(0, now.Load()) }

	impl.Set("k", []byte("v"), 10*time.Millisecond)
	if _, ok := impl.Get("k"); !ok {
		t.Fatal("entry should be present before expiry")
	}

	now.Add(int64(time.Second))
	if _, ok := impl.Get("k"); ok {
		t.Fatal("entry should have expired")
	}
}

func TestCache_ZeroTTL_NoExpiry(t *testing.T) {
	impl := DefaultCache(10, 0).(*lruCache)

	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	impl.clock = func() time.Time { return time.Unix(0, now.Load()) }

	impl.Set("k", []byte("v"), 0)
	now.Add(int64(time.Hour))
	if _, ok := impl.Get("k"); !ok {
		t.Fatal("zero TTL entry should never expire")
	}
}

func TestCache_Invalidate(t *testing.T) {
	c := DefaultCache(10, time.Minute)
	c.Set("a", []byte("1"), time.Minute)
	c.Set("b", []byte("2"), time.Minute)

	c.Invalidate("a")
	if _, ok := c.Get("a"); ok {
		t.Error("a should be gone")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should still be present")
	}

	c.InvalidateAll()
	if _, ok := c.Get("b"); ok {
		t.Error("InvalidateAll should clear everything")
	}
}
