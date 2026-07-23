// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit_test

import (
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/ratelimit"
)

func TestKeyed_BurstAndRefillPerKey(t *testing.T) {
	now := time.Unix(1000, 0)
	k := ratelimit.NewKeyed(ratelimit.Spec{RPS: 1, Burst: 2}, 0, func() time.Time { return now })

	for i := range 2 {
		if !k.Allow("a") {
			t.Fatalf("request %d within burst must be allowed", i+1)
		}
	}
	if k.Allow("a") {
		t.Fatal("third request must be denied (burst exhausted)")
	}
	// A different key has its own bucket.
	if !k.Allow("b") {
		t.Fatal("independent key must have its own bucket")
	}
	now = now.Add(time.Second)
	if !k.Allow("a") {
		t.Fatal("after 1s one token should have refilled")
	}
	if k.Allow("a") {
		t.Fatal("only one token should have refilled")
	}
}

func TestKeyed_ZeroRPSPermitsEverything(t *testing.T) {
	k := ratelimit.NewKeyed(ratelimit.Spec{}, 0, func() time.Time { return time.Unix(0, 0) })
	for range 100 {
		if !k.Allow("x") {
			t.Fatal("zero-RPS spec must permit everything")
		}
	}
}

func TestKeyed_EvictionBoundsKeys(t *testing.T) {
	now := time.Unix(0, 0)
	k := ratelimit.NewKeyed(ratelimit.Spec{RPS: 1, Burst: 1}, 2, func() time.Time { return now })

	if !k.Allow("k1") {
		t.Fatal("k1 first request allowed")
	}
	if k.Allow("k1") {
		t.Fatal("k1 second request denied (burst 1)")
	}
	// Two more keys evict k1 from the size-2 LRU; a returning k1 starts
	// over with a fresh burst — the map never exceeds maxKeys.
	if !k.Allow("k2") || !k.Allow("k3") {
		t.Fatal("fresh keys must be allowed")
	}
	if !k.Allow("k1") {
		t.Fatal("evicted key must restart with a fresh burst")
	}
}

func TestKeyed_UnsetBurstDefaultsToRPS(t *testing.T) {
	now := time.Unix(0, 0)
	k := ratelimit.NewKeyed(ratelimit.Spec{RPS: 3}, 0, func() time.Time { return now })
	for i := range 3 {
		if !k.Allow("x") {
			t.Fatalf("request %d within defaulted burst must be allowed", i+1)
		}
	}
	if k.Allow("x") {
		t.Fatal("request beyond defaulted burst must be denied")
	}
}
