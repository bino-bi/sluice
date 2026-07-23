// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type recordingNotifier struct {
	mu     sync.Mutex
	calls  int
	accept string
	reject string
	id     string
}

func (n *recordingNotifier) Notify(v View, acceptURL, rejectURL string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.accept, n.reject, n.id = acceptURL, rejectURL, v.ID
}

func newBroker(t *testing.T, clk *fakeClock, n Notifier) *Broker {
	t.Helper()
	b, err := New(Options{
		Clock:      clk.now,
		Notifier:   n,
		RequestTTL: 10 * time.Minute,
		GrantTTL:   5 * time.Minute,
		MaxPending: 3,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func requireInput(subject, sql string) RequireInput {
	return RequireInput{
		Subject:    Subject{ID: subject},
		SubjectKey: "iss\x00" + subject,
		SQLHash:    "hash-" + sql,
		SQLSample:  sql,
		Reasons:    []string{"needs approval"},
		Policies:   []string{"gate"},
	}
}

func TestBroker_AcceptFlow(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b := newBroker(t, clk, n)

	ticket, created, err := b.Require(context.Background(), requireInput("alice", "SELECT 1"))
	if err != nil || !created {
		t.Fatalf("Require: created=%v err=%v", created, err)
	}
	if n.calls != 1 {
		t.Fatalf("webhook fired %d times, want 1", n.calls)
	}

	// Token derived from the notifier's accept URL (?token=...).
	token := tokenFromURL(n.accept)
	res, err := b.Accept(ticket.ID, token, "1.2.3.4")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if res.State != StateApproved {
		t.Errorf("state = %s, want approved", res.State)
	}

	// Grant is consumable exactly once.
	in := requireInput("alice", "SELECT 1")
	id, ok := b.ConsumeGrant(in.SubjectKey, in.SQLHash)
	if !ok || id != ticket.ID {
		t.Fatalf("ConsumeGrant miss: ok=%v id=%q", ok, id)
	}
	if _, ok := b.ConsumeGrant(in.SubjectKey, in.SQLHash); ok {
		t.Error("grant consumed twice")
	}
}

func TestBroker_DedupeNoDoubleWebhook(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b := newBroker(t, clk, n)

	t1, created1, _ := b.Require(context.Background(), requireInput("alice", "SELECT 1"))
	t2, created2, _ := b.Require(context.Background(), requireInput("alice", "SELECT 1"))
	if !created1 || created2 {
		t.Fatalf("created flags = %v/%v, want true/false", created1, created2)
	}
	if t1.ID != t2.ID {
		t.Errorf("dedupe returned different ids: %s vs %s", t1.ID, t2.ID)
	}
	if n.calls != 1 {
		t.Errorf("webhook fired %d times, want 1 (dedupe)", n.calls)
	}
}

func TestBroker_MaxPending(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	b := newBroker(t, clk, &recordingNotifier{})
	for i, sql := range []string{"a", "b", "c"} {
		if _, _, err := b.Require(context.Background(), requireInput("u", sql)); err != nil {
			t.Fatalf("Require %d: %v", i, err)
		}
	}
	if _, _, err := b.Require(context.Background(), requireInput("u", "d")); !errors.Is(err, ErrBrokerFull) {
		t.Fatalf("4th Require err = %v, want ErrBrokerFull", err)
	}
}

func TestBroker_RejectAndWrongToken(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b := newBroker(t, clk, n)
	ticket, _, _ := b.Require(context.Background(), requireInput("alice", "x"))

	if _, err := b.Accept(ticket.ID, "wrong-token", ""); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("wrong token err = %v, want ErrTokenMismatch", err)
	}
	if _, err := b.Reject(ticket.ID, tokenFromURL(n.reject), "9.9.9.9"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	// Idempotent reject.
	res, err := b.Reject(ticket.ID, tokenFromURL(n.reject), "9.9.9.9")
	if err != nil || !res.AlreadyDecided {
		t.Errorf("second reject: res=%+v err=%v", res, err)
	}
}

func TestBroker_WaitWakesOnDecision(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b := newBroker(t, clk, n)
	ticket, _, _ := b.Require(context.Background(), requireInput("alice", "x"))

	var state atomic.Value
	done := make(chan struct{})
	go func() {
		st, _ := b.Wait(context.Background(), ticket.ID, 5*time.Second)
		state.Store(st)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := b.Accept(ticket.ID, tokenFromURL(n.accept), ""); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not wake on decision")
	}
	if state.Load() != StateApproved {
		t.Errorf("Wait returned %v, want approved", state.Load())
	}
}

func TestBroker_Expiry(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	b := newBroker(t, clk, &recordingNotifier{})
	ticket, _, _ := b.Require(context.Background(), requireInput("alice", "x"))

	clk.advance(11 * time.Minute)
	st, err := b.Wait(context.Background(), ticket.ID, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if st != StateExpired {
		t.Errorf("state = %s, want expired", st)
	}
}

func TestBroker_ConcurrentConsumeGrantExactlyOnce(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b := newBroker(t, clk, n)
	in := requireInput("alice", "x")
	ticket, _, _ := b.Require(context.Background(), in)
	if _, err := b.Accept(ticket.ID, tokenFromURL(n.accept), ""); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	var winners atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, ok := b.ConsumeGrant(in.SubjectKey, in.SQLHash); ok {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("grant consumed %d times, want exactly 1", winners.Load())
	}
}

// tokenFromURL extracts the token query value from a capability URL.
func tokenFromURL(u string) string {
	i := len("?token=")
	idx := indexOf(u, "?token=")
	if idx < 0 {
		return u
	}
	return u[idx+i:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
