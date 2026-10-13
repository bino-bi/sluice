// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/tlsutil"
	"github.com/bino-bi/sluice/internal/transport/tlsutil/tlstest"
)

// An empty address would bind port 80 on every interface.
func TestNew_HTTPNeedsListen(t *testing.T) {
	_, err := New(Config{Transport: TransportStreamableHTTP}, Deps{Service: testService()})
	if err == nil || !strings.Contains(err.Error(), "mcp.listen") {
		t.Fatalf("err = %v, want an error that names mcp.listen", err)
	}
	if _, err := New(Config{Transport: TransportStdio}, Deps{Service: testService()}); err != nil {
		t.Fatalf("stdio needs no address: %v", err)
	}
}

// bucket refuses every key in refuse and records the keys it was asked for.
type bucket struct {
	refuse map[string]bool
	asked  []string
}

func (b *bucket) Allow(key string) bool {
	b.asked = append(b.asked, key)
	return !b.refuse[key]
}

// countingIdentifier counts the logins, so a test can show that a refused
// request never reached the identifier.
type countingIdentifier struct {
	keySubjectIdentifier
	calls int
}

func (c *countingIdentifier) Identify(ctx context.Context, r *http.Request) (*identity.UserCtx, error) {
	c.calls++
	return c.keySubjectIdentifier.Identify(ctx, r)
}

func TestStreamable_RateLimitRunsBeforeLogin(t *testing.T) {
	for _, tc := range []struct {
		name          string
		global, perIP *bucket
		wantStatus    int
		wantLogins    int
	}{
		{"global bucket empty", &bucket{refuse: map[string]bool{"": true}}, &bucket{}, http.StatusTooManyRequests, 0},
		{"bucket of the client address empty", &bucket{}, &bucket{refuse: map[string]bool{"127.0.0.1": true}}, http.StatusTooManyRequests, 0},
		{"both have room", &bucket{}, &bucket{}, http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := &countingIdentifier{}
			_, url := newStreamableTestServerWith(t, Config{}, func(d *Deps) {
				d.Identifier = id
				d.GlobalLimiter = tc.global
				d.PerIPLimiter = tc.perIP
			})
			status, _, body := mcpPost(t, url, "alice", "", rpcInitialize)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", status, tc.wantStatus, body)
			}
			if id.calls != tc.wantLogins {
				t.Errorf("logins = %d, want %d", id.calls, tc.wantLogins)
			}
			if tc.wantStatus == http.StatusTooManyRequests && !strings.Contains(body, `"code":"ERR_RATE_LIMITED"`) {
				t.Errorf("body = %s, want ERR_RATE_LIMITED", body)
			}
		})
	}
}

// The bucket of a client is keyed by its address without the port.
func TestStreamable_PerIPBucketKey(t *testing.T) {
	perIP := &bucket{}
	_, url := newStreamableTestServerWith(t, Config{}, func(d *Deps) { d.PerIPLimiter = perIP })
	mcpPost(t, url, "alice", "", rpcInitialize)
	if len(perIP.asked) != 1 || perIP.asked[0] != "127.0.0.1" {
		t.Errorf("bucket keys = %v, want [127.0.0.1]", perIP.asked)
	}
}

// freeAddr returns a loopback address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestRun_ServesTLS(t *testing.T) {
	ca := tlstest.NewCA(t, "server")
	cert, key := ca.ServerCert(t)
	addr := freeAddr(t)
	s, err := New(Config{
		Transport:  TransportStreamableHTTP,
		HTTPListen: addr,
		TLS:        &tlsutil.Config{CertFile: cert, KeyFile: key},
	}, Deps{Service: testService(), Identifier: keySubjectIdentifier{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: ca.Pool(t), MinVersion: tls.VersionTLS12}},
	}
	// Without a credential the answer is 401, over TLS.
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = client.Get("https://" + addr + "/")
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("https request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// The listener speaks no plain HTTP.
	plain, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + addr + "/")
	if err == nil {
		_ = plain.Body.Close()
		if plain.StatusCode != http.StatusBadRequest {
			t.Errorf("plain HTTP status = %d, want 400 from the TLS listener", plain.StatusCode)
		}
	}
}

func TestRun_BadTLSFailsBeforeBind(t *testing.T) {
	s, err := New(Config{
		Transport:  TransportStreamableHTTP,
		HTTPListen: "127.0.0.1:0",
		TLS:        &tlsutil.Config{CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"},
	}, Deps{Service: testService()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "tls") {
		t.Fatalf("Run = %v, want a tls error", err)
	}
}
