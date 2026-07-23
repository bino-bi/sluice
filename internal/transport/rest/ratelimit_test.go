// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/ratelimit"
	"github.com/bino-bi/sluice/internal/transport/rest"
)

// countingIdentifier records how often the identity layer is reached so a
// test can prove the rate limiter denies before authentication.
type countingIdentifier struct {
	calls int
}

func (c *countingIdentifier) Identify(_ context.Context, _ *http.Request) (*identity.UserCtx, error) {
	c.calls++
	return nil, identity.ErrNoCredential
}

func (c *countingIdentifier) Name() string { return "counting" }

func postQuery(srv *rest.Server, remoteAddr string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/query", strings.NewReader(`{"sql":"SELECT 1"}`))
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	return postRaw(srv, w, r)
}

func postRaw(srv *rest.Server, w *httptest.ResponseRecorder, r *http.Request) *httptest.ResponseRecorder {
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestHandleQuery_GlobalRateLimit429BeforeIdentity(t *testing.T) {
	t.Parallel()
	id := &countingIdentifier{}
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier:    id,
		GlobalLimiter: ratelimit.NewKeyed(ratelimit.Spec{RPS: 1, Burst: 1}, 1, func() time.Time { return time.Unix(0, 0) }),
	})

	if w := postQuery(srv, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("first request: got %d want 401 (passes limiter, fails auth)", w.Code)
	}
	w := postQuery(srv, "")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d want 429", w.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "ERR_RATE_LIMITED" {
		t.Fatalf("error code: got %q want ERR_RATE_LIMITED", body.Code)
	}
	if id.calls != 1 {
		t.Fatalf("identifier calls: got %d want 1 — the 429 must happen before identity", id.calls)
	}
}

func TestHandleQuery_PerIPBucketsAreIndependent(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier:   &stubIdentifier{err: identity.ErrNoCredential},
		PerIPLimiter: ratelimit.NewKeyed(ratelimit.Spec{RPS: 1, Burst: 1}, 100, func() time.Time { return time.Unix(0, 0) }),
	})

	if w := postQuery(srv, "198.51.100.7:4711"); w.Code != http.StatusUnauthorized {
		t.Fatalf("ip1 first request: got %d want 401", w.Code)
	}
	if w := postQuery(srv, "198.51.100.7:4712"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("ip1 second request (other port, same host): got %d want 429", w.Code)
	}
	if w := postQuery(srv, "198.51.100.8:4711"); w.Code != http.StatusUnauthorized {
		t.Fatalf("ip2 first request: got %d want 401 — buckets must be per IP", w.Code)
	}
}

func TestHandleQuery_HealthNeverRateLimited(t *testing.T) {
	t.Parallel()
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier:    &stubIdentifier{},
		GlobalLimiter: ratelimit.NewKeyed(ratelimit.Spec{RPS: 1, Burst: 1}, 1, func() time.Time { return time.Unix(0, 0) }),
	})
	// Exhaust the global bucket via the data plane.
	_ = postQuery(srv, "")
	_ = postQuery(srv, "")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d want 200 — probes must not be rate limited", w.Code)
	}
}
