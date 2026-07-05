// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testView() View {
	return View{
		ID:        "01ABCDEF",
		Subject:   Subject{ID: "alice", Issuer: "iss", Groups: []string{"analysts"}},
		SQLSample: "SELECT ssn FROM hr.people",
		Reasons:   []string{"PII access"},
		Policies:  []string{"approve-pii"},
		CreatedAt: time.Unix(1_700_000_000, 0),
		ExpiresAt: time.Unix(1_700_000_900, 0),
	}
}

func TestWebhook_PayloadAndCapabilityURLs(t *testing.T) {
	var got payload
	var gotAuth string
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		close(done)
	}))
	defer srv.Close()

	n := NewWebhookNotifier("https://sluice.example.com/", []Target{
		{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}},
	}, nil)
	n.Notify(testView(), "acc-token", "rej-token")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered")
	}

	if got.ApprovalID != "01ABCDEF" || got.Subject.ID != "alice" {
		t.Errorf("payload subject wrong: %+v", got)
	}
	if !strings.HasPrefix(got.AcceptURL, "https://sluice.example.com/v1/approvals/01ABCDEF/accept?token=acc-token") {
		t.Errorf("accept URL wrong: %q", got.AcceptURL)
	}
	if !strings.Contains(got.RejectURL, "/reject?token=rej-token") {
		t.Errorf("reject URL wrong: %q", got.RejectURL)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth header not forwarded: %q", gotAuth)
	}
}

func TestWebhook_RetryThenSucceed(t *testing.T) {
	var hits atomic.Int64
	done := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		once.Do(func() { close(done) })
	}))
	defer srv.Close()

	n := NewWebhookNotifier("https://x", []Target{{URL: srv.URL, Timeout: time.Second}}, nil)
	// Shrink backoff by delivering directly.
	n.Notify(testView(), "a", "r")

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("webhook did not succeed after retries")
	}
	if hits.Load() != 3 {
		t.Errorf("delivery attempts = %d, want 3 (2 failures + 1 success)", hits.Load())
	}
}

func TestWebhook_MultipleTargets(t *testing.T) {
	var a, b atomic.Int64
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { a.Add(1) }))
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { b.Add(1) }))
	defer sa.Close()
	defer sb.Close()

	n := NewWebhookNotifier("https://x", []Target{{URL: sa.URL}, {URL: sb.URL}}, nil)
	n.Notify(testView(), "a", "r")

	deadline := time.After(2 * time.Second)
	for a.Load() == 0 || b.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("targets not both hit: a=%d b=%d", a.Load(), b.Load())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
