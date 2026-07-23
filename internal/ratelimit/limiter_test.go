// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit_test

import (
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/ratelimit"
)

func TestLimiter_PerSubjectBurstAndRefill(t *testing.T) {
	now := time.Unix(1000, 0)
	l := ratelimit.New(func() time.Time { return now })
	l.SetSpecs(map[string]ratelimit.Spec{"alice": {RPS: 1, Burst: 2}}, nil)

	first := l.Allow("alice", "")
	second := l.Allow("alice", "")
	if !first || !second {
		t.Fatal("first two requests within burst must be allowed")
	}
	if l.Allow("alice", "") {
		t.Fatal("third request must be denied (burst exhausted)")
	}

	now = now.Add(time.Second) // refill one token
	if !l.Allow("alice", "") {
		t.Fatal("after 1s, one token should be available")
	}
	if l.Allow("alice", "") {
		t.Fatal("only one token should have refilled")
	}
}

func TestLimiter_UnconfiguredAndAnonymousAreUnlimited(t *testing.T) {
	l := ratelimit.New(func() time.Time { return time.Unix(0, 0) })
	l.SetSpecs(map[string]ratelimit.Spec{"alice": {RPS: 1, Burst: 1}}, nil)

	for range 100 {
		if !l.Allow("bob", "") {
			t.Fatal("unconfigured subject must be unlimited")
		}
		if !l.Allow("", "iss") {
			t.Fatal("empty subject (anonymous) must be unlimited")
		}
	}
}

func TestLimiter_DefaultSpecForUnboundSubjects(t *testing.T) {
	now := time.Unix(0, 0)
	l := ratelimit.New(func() time.Time { return now })
	l.SetDefault(ratelimit.Spec{RPS: 1, Burst: 1})
	l.SetSpecs(map[string]ratelimit.Spec{"alice": {RPS: 1, Burst: 3}}, nil)

	// Unbound subject falls back to the default spec.
	if !l.Allow("bob", "") {
		t.Fatal("bob first request allowed under default spec")
	}
	if l.Allow("bob", "") {
		t.Fatal("bob second request denied (default burst 1)")
	}
	// A binding-level spec overrides the default.
	for i := range 3 {
		if !l.Allow("alice", "") {
			t.Fatalf("alice request %d within binding burst must be allowed", i+1)
		}
	}
	// Anonymous stays unlimited even with a default set.
	for range 10 {
		if !l.Allow("", "") {
			t.Fatal("anonymous must stay unlimited")
		}
	}
	// A reload (SetSpecs) preserves the default.
	l.SetSpecs(nil, nil)
	if !l.Allow("carol", "") {
		t.Fatal("carol first request allowed under default spec")
	}
	if l.Allow("carol", "") {
		t.Fatal("default spec must survive SetSpecs")
	}
}

func TestLimiter_PerIssuerAppliesPerSubject(t *testing.T) {
	now := time.Unix(0, 0)
	l := ratelimit.New(func() time.Time { return now })
	l.SetSpecs(nil, map[string]ratelimit.Spec{"iss1": {RPS: 1, Burst: 1}})

	if !l.Allow("user1", "iss1") {
		t.Fatal("user1 first request allowed")
	}
	if l.Allow("user1", "iss1") {
		t.Fatal("user1 second request denied (burst 1)")
	}
	// A different subject under the same issuer has its own bucket.
	if !l.Allow("user2", "iss1") {
		t.Fatal("user2 must have its own bucket under the issuer spec")
	}
}
