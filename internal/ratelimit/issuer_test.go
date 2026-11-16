// SPDX-License-Identifier: AGPL-3.0-or-later

package ratelimit_test

import (
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/ratelimit"
)

// Two issuers that both issue sub alice get one bucket each. Before, they
// shared one, and with two specs each request replaced it with a full one.
func TestLimiter_IssuersSharingASubGetOwnBuckets(t *testing.T) {
	now := time.Unix(1000, 0)
	l := ratelimit.New(func() time.Time { return now })
	l.SetSpecs(nil, map[string]ratelimit.Spec{
		"iss-a": {RPS: 1, Burst: 1},
		"iss-b": {RPS: 1, Burst: 2},
	})
	allowed := 0
	for range 10 {
		if l.Allow("alice", "iss-a") {
			allowed++
		}
		if l.Allow("alice", "iss-b") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed %d of 20 alternating requests, want 3 (burst 1 plus burst 2)", allowed)
	}
}

// A subject spec of one issuer does not apply to the same sub of another
// issuer. A subject spec without an issuer applies to every issuer.
func TestLimiter_SubjectSpecIsIssuerAware(t *testing.T) {
	l := ratelimit.New(func() time.Time { return time.Unix(0, 0) })
	l.SetSpecs(map[ratelimit.SubjectKey]ratelimit.Spec{
		{Subject: "alice", Issuer: "keys"}: {RPS: 1, Burst: 1},
		{Subject: "bob"}:                   {RPS: 1, Burst: 1},
	}, nil)
	if !l.Allow("alice", "keys") || l.Allow("alice", "keys") {
		t.Fatal("alice of issuer keys: want burst 1")
	}
	for range 5 {
		if !l.Allow("alice", "idp") {
			t.Fatal("alice of another issuer got the limit of issuer keys")
		}
	}
	if !l.Allow("bob", "idp") || l.Allow("bob", "idp") {
		t.Fatal("bob without issuer in the spec: want burst 1 for every issuer")
	}
}
