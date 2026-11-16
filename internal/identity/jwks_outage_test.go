// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
)

// fakeClock is a clock that tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// After a failed refresh the client serves the stale key and does not
// fetch again until minRefresh has passed.
func TestJWKSClientBacksOffAfterFailedRefresh(t *testing.T) {
	srv := newJWKSServer(t, genRSAPub(t))
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Minute,
		MinRefresh: 30 * time.Second,
		Clock:      clk.Now,
	})
	ctx := context.Background()
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}

	srv.failing.Store(true)
	clk.Add(2 * time.Minute) // stale
	for range 5 {
		if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
			t.Fatalf("stale key: %v", err)
		}
	}
	if got := srv.fetchCount.Load(); got != 2 {
		t.Fatalf("fetchCount = %d, want 2: one prime, one failed refresh", got)
	}

	clk.Add(31 * time.Second)
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("stale key after backoff: %v", err)
	}
	if got := srv.fetchCount.Load(); got != 3 {
		t.Fatalf("fetchCount = %d, want 3 after the backoff window", got)
	}
}

// Stale keys are served for at most MaxJWKSStaleAge past the cache time.
// After that the issuer fails closed.
func TestJWKSClientFailsClosedAfterMaxStaleAge(t *testing.T) {
	srv := newJWKSServer(t, genRSAPub(t))
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Minute,
		Clock:      clk.Now,
	})
	ctx := context.Background()
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}
	srv.failing.Store(true)

	clk.Add(time.Minute + identity.MaxJWKSStaleAge - time.Second)
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("within max stale age: %v", err)
	}
	clk.Add(time.Hour)
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); !errors.Is(err, identity.ErrJWKSUnreachable) {
		t.Fatalf("past max stale age: err = %v, want ErrJWKSUnreachable", err)
	}
}

// A first fetch that fails is cached too: the next requests do not hit
// the endpoint until minRefresh has passed.
func TestJWKSClientNegativeCache(t *testing.T) {
	srv := newJWKSServer(t, genRSAPub(t))
	srv.failing.Store(true)
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		MinRefresh: 30 * time.Second,
		Clock:      clk.Now,
	})
	ctx := context.Background()
	for range 3 {
		if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); !errors.Is(err, identity.ErrJWKSUnreachable) {
			t.Fatalf("err = %v, want ErrJWKSUnreachable", err)
		}
	}
	if got := srv.fetchCount.Load(); got != 1 {
		t.Fatalf("fetchCount = %d, want 1", got)
	}

	srv.failing.Store(false)
	clk.Add(31 * time.Second)
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
}

// While one request waits on a hanging JWKS endpoint, other requests do
// not queue behind it: with a stale key they get it at once, without one
// they leave when their context ends.
func TestJWKSClientHangingRefreshDoesNotBlockOthers(t *testing.T) {
	srv := newJWKSServer(t, genRSAPub(t))
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Minute,
		Clock:      clk.Now,
	})
	ctx := context.Background()
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}

	release := make(chan struct{})
	srv.hang.Store(&release)
	t.Cleanup(func() { close(release) })
	clk.Add(2 * time.Minute) // stale

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		_, _, _ = client.KeyFor(ctx, srv.URL, "key-0", 0)
	}()
	<-started
	waitFor(t, func() bool { return srv.fetchCount.Load() == 2 })

	// Stale key: served at once.
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("stale key during refresh: %v", err)
	}

	// Unknown kid: waits for the lock, but only as long as its context.
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := client.KeyFor(short, srv.URL, "other", 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("waited %s, want about 50ms", waited)
	}

	release <- struct{}{}
	<-done
}

// A fetch that runs into the request deadline counts as a failure, so a
// hanging endpoint is backed off like one that answers 503.
func TestJWKSClientBacksOffAfterDeadline(t *testing.T) {
	srv := newJWKSServer(t, genRSAPub(t))
	release := make(chan struct{})
	srv.hang.Store(&release)
	t.Cleanup(func() { close(release) })
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	client := identity.NewJWKSClient(identity.JWKSClientOptions{Clock: clk.Now})

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := client.KeyFor(short, srv.URL, "key-0", 0); err == nil {
		t.Fatal("hanging fetch: want an error")
	}
	if _, _, err := client.KeyFor(context.Background(), srv.URL, "key-0", 0); !errors.Is(err, identity.ErrJWKSUnreachable) {
		t.Fatalf("err = %v, want ErrJWKSUnreachable from the backoff", err)
	}
	if got := srv.fetchCount.Load(); got != 1 {
		t.Fatalf("fetchCount = %d, want 1", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(time.Millisecond)
	}
}
