// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func persistedBroker(t *testing.T, clk *fakeClock, n Notifier, path string) *Broker {
	t.Helper()
	store, err := NewSQLiteStore(path, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	b, err := New(Options{
		Clock:      clk.now,
		Notifier:   n,
		Store:      store,
		RequestTTL: 10 * time.Minute,
		GrantTTL:   5 * time.Minute,
		MaxPending: 10,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func TestBroker_RestoresPendingAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.db")
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}

	a := persistedBroker(t, clk, n, path)
	tk, created, err := a.Require(context.Background(), requireInput("alice", "SELECT ssn FROM t"))
	if err != nil || !created {
		t.Fatalf("Require: created=%v err=%v", created, err)
	}
	acceptToken := n.accept
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("close broker A: %v", err)
	}

	// "Restart": a fresh broker on the same database.
	b := persistedBroker(t, clk, &recordingNotifier{}, path)
	v, ok := b.Get(tk.ID)
	if !ok || v.State != StatePending {
		t.Fatalf("restored request = (%+v, %v), want pending", v, ok)
	}
	// Dedupe still holds: the same subject+SQL returns the old ticket.
	tk2, created2, err := b.Require(context.Background(), requireInput("alice", "SELECT ssn FROM t"))
	if err != nil || created2 || tk2.ID != tk.ID {
		t.Fatalf("dedupe after restart: id=%s created=%v err=%v, want id=%s created=false", tk2.ID, created2, err, tk.ID)
	}
	// The ORIGINAL capability token still works — hashing is deterministic.
	if _, err := b.Accept(tk.ID, acceptToken, "10.0.0.9"); err != nil {
		t.Fatalf("Accept with pre-restart token: %v", err)
	}
	if id, ok := b.ConsumeGrant("iss\x00alice", "hash-SELECT ssn FROM t"); !ok || id != tk.ID {
		t.Fatalf("ConsumeGrant = (%s, %v), want (%s, true)", id, ok, tk.ID)
	}
}

func TestBroker_GrantSurvivesRestartExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.db")
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}

	a := persistedBroker(t, clk, n, path)
	tk, _, err := a.Require(context.Background(), requireInput("bob", "SELECT 1"))
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	if _, err := a.Accept(tk.ID, n.accept, ""); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("close broker A: %v", err)
	}

	b := persistedBroker(t, clk, &recordingNotifier{}, path)
	if id, ok := b.ConsumeGrant("iss\x00bob", "hash-SELECT 1"); !ok || id != tk.ID {
		t.Fatalf("grant must survive restart: (%s, %v)", id, ok)
	}
	if _, ok := b.ConsumeGrant("iss\x00bob", "hash-SELECT 1"); ok {
		t.Fatal("grant must be single-use in the same process")
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close broker B: %v", err)
	}

	// Third broker: consumption must have persisted synchronously.
	c := persistedBroker(t, clk, &recordingNotifier{}, path)
	if _, ok := c.ConsumeGrant("iss\x00bob", "hash-SELECT 1"); ok {
		t.Fatal("consumed grant must not be replayable after restart")
	}
	if v, ok := c.Get(tk.ID); !ok || v.State != StateConsumed {
		t.Fatalf("restored request state = (%+v, %v), want consumed", v, ok)
	}
}

func TestBroker_ExpiredPendingNotResurrected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.db")
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}

	a := persistedBroker(t, clk, n, path)
	tk, _, err := a.Require(context.Background(), requireInput("carol", "SELECT 2"))
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	token := n.accept
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("close broker A: %v", err)
	}

	clk.advance(11 * time.Minute) // past RequestTTL
	b := persistedBroker(t, clk, &recordingNotifier{}, path)
	if v, ok := b.Get(tk.ID); ok && v.State == StatePending {
		t.Fatalf("request restored as pending past its expiry: %+v", v)
	}
	if _, err := b.Accept(tk.ID, token, ""); err == nil {
		t.Fatal("accepting an expired request must fail")
	}
}

func TestBroker_PlaintextTokenAbsentFromDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.db")
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}

	b := persistedBroker(t, clk, n, path)
	if _, _, err := b.Require(context.Background(), requireInput("dave", "SELECT 3")); err != nil {
		t.Fatalf("Require: %v", err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	for _, wal := range []string{path + "-wal", path + "-shm"} {
		if extra, err := os.ReadFile(wal); err == nil {
			raw = append(raw, extra...)
		}
	}
	for name, tok := range map[string]string{"accept": n.accept, "reject": n.reject} {
		if tok == "" {
			t.Fatalf("notifier captured no %s token", name)
		}
		if strings.Contains(string(raw), tok) {
			t.Fatalf("plaintext %s token must not appear in the database file", name)
		}
		if !strings.Contains(string(raw), hashToken(tok)) {
			t.Fatalf("sha256 of the %s token should be stored", name)
		}
	}
}

// erroringStore fails every mutation; Load succeeds empty.
type erroringStore struct{}

func (erroringStore) Load(context.Context) ([]StoredRequest, []StoredGrant, error) {
	return nil, nil, nil
}
func (erroringStore) PutRequest(context.Context, StoredRequest) error {
	return errors.New("disk full")
}
func (erroringStore) DeleteRequest(context.Context, string) error { return errors.New("disk full") }
func (erroringStore) PutGrant(context.Context, StoredGrant) error { return errors.New("disk full") }
func (erroringStore) DeleteGrant(context.Context, string) error   { return errors.New("disk full") }
func (erroringStore) Close(context.Context) error                 { return nil }

func TestBroker_StoreWriteFailureFailsClosed(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	n := &recordingNotifier{}
	b, err := New(Options{Clock: clk.now, Notifier: n, Store: erroringStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := b.Require(context.Background(), requireInput("erin", "SELECT 4")); err == nil {
		t.Fatal("Require must fail when the request cannot be persisted")
	}
	if n.calls != 0 {
		t.Fatalf("webhook fired %d times for a non-durable request, want 0", n.calls)
	}
}
