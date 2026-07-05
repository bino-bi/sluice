// SPDX-License-Identifier: AGPL-3.0-or-later

package policycache

import (
	"net"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
)

func TestCachePutGet(t *testing.T) {
	c := New(8, time.Minute)
	user := &identity.UserCtx{Subject: "alice"}
	k := BuildKey(1, "d", "SELECT 1", user, nil, nil, false)
	entry := &Entry{Decision: &policy.Decision{Outcome: policy.OutcomeAllow}, Rewrite: &rewriter.RewriteResult{SQL: "SELECT 1"}}
	c.Put(k, entry)

	got, ok := c.Get(k)
	if !ok || got != entry {
		t.Fatalf("Get miss or wrong entry: ok=%v", ok)
	}
}

func TestKeyDependsOnRawSQLNotFingerprint(t *testing.T) {
	// Two queries that share a pg_query fingerprint (differ only in the
	// literal) must produce different keys — the raw SQL text is hashed.
	user := &identity.UserCtx{Subject: "alice"}
	k1 := BuildKey(1, "d", "SELECT * FROM t WHERE id = 1", user, nil, nil, false)
	k2 := BuildKey(1, "d", "SELECT * FROM t WHERE id = 2", user, nil, nil, false)
	if k1 == k2 {
		t.Fatal("keys collide for different literals — cache would replay wrong SQL")
	}
}

func TestKeyDependsOnIdentity(t *testing.T) {
	a := &identity.UserCtx{Subject: "alice", Claims: map[string]any{"tenant": "acme"}}
	b := &identity.UserCtx{Subject: "alice", Claims: map[string]any{"tenant": "globex"}}
	sql := "SELECT * FROM t"
	if BuildKey(1, "d", sql, a, nil, nil, false) == BuildKey(1, "d", sql, b, nil, nil, false) {
		t.Fatal("keys collide across differing claims")
	}
}

func TestKeyGroupsStableRegardlessOfOrder(t *testing.T) {
	a := &identity.UserCtx{Subject: "u", Groups: []string{"x", "y"}}
	b := &identity.UserCtx{Subject: "u", Groups: []string{"y", "x"}}
	sql := "SELECT 1"
	if BuildKey(1, "d", sql, a, nil, nil, false) != BuildKey(1, "d", sql, b, nil, nil, false) {
		t.Fatal("group order must not change the key")
	}
}

func TestKeyHeaderScoping(t *testing.T) {
	sql := "SELECT 1"
	user := &identity.UserCtx{Subject: "u"}
	f1 := &policy.RequestFacts{Headers: map[string]string{"x-region": "eu", "x-req-id": "1"}}
	f2 := &policy.RequestFacts{Headers: map[string]string{"x-region": "eu", "x-req-id": "2"}}

	// Only x-region is referenced: the volatile x-req-id must not split keys.
	if BuildKey(1, "d", sql, user, f1, []string{"x-region"}, false) !=
		BuildKey(1, "d", sql, user, f2, []string{"x-region"}, false) {
		t.Error("unreferenced header changed the key")
	}
	// A referenced header value change splits keys.
	f3 := &policy.RequestFacts{Headers: map[string]string{"x-region": "us"}}
	if BuildKey(1, "d", sql, user, f1, []string{"x-region"}, false) ==
		BuildKey(1, "d", sql, user, f3, []string{"x-region"}, false) {
		t.Error("referenced header change did not split keys")
	}
	// allHeaders mode keys on everything.
	if BuildKey(1, "d", sql, user, f1, nil, true) ==
		BuildKey(1, "d", sql, user, f2, nil, true) {
		t.Error("allHeaders mode ignored a differing header")
	}
}

func TestKeyVersionInvalidatesEntries(t *testing.T) {
	c := New(8, time.Minute)
	user := &identity.UserCtx{Subject: "u"}
	k1 := BuildKey(1, "d1", "SELECT 1", user, nil, nil, false)
	c.Put(k1, &Entry{Decision: &policy.Decision{}})
	// A new snapshot (version 2, digest d2) yields a different key → miss.
	k2 := BuildKey(2, "d2", "SELECT 1", user, nil, nil, false)
	if _, ok := c.Get(k2); ok {
		t.Fatal("stale entry reachable after version bump")
	}
}

func TestPurge(t *testing.T) {
	c := New(8, time.Minute)
	k := BuildKey(1, "d", "SELECT 1", nil, nil, nil, false)
	c.Put(k, &Entry{Decision: &policy.Decision{}})
	c.Purge()
	if _, ok := c.Get(k); ok {
		t.Fatal("entry survived Purge")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := New(8, 30*time.Millisecond)
	k := BuildKey(1, "d", "SELECT 1", nil, nil, nil, false)
	c.Put(k, &Entry{Decision: &policy.Decision{}})
	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get(k); ok {
		t.Fatal("entry survived its TTL")
	}
}

func TestRemoteIPParticipates(t *testing.T) {
	sql := "SELECT 1"
	user := &identity.UserCtx{Subject: "u"}
	f1 := &policy.RequestFacts{RemoteIP: net.ParseIP("10.0.0.1")}
	f2 := &policy.RequestFacts{RemoteIP: net.ParseIP("10.0.0.2")}
	if BuildKey(1, "d", sql, user, f1, nil, false) == BuildKey(1, "d", sql, user, f2, nil, false) {
		t.Fatal("remote IP did not participate in the key")
	}
}
