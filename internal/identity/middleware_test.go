// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

func TestMiddlewareRejectsMissingCredential(t *testing.T) {
	mw := identity.Middleware(identity.MiddlewareOptions{
		Identifier: &stubIdentifier{name: "x", err: identity.ErrNoCredential},
	})
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run without credential")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Code = %d; want 401", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("WWW-Authenticate header missing")
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "ERR_UNAUTHORIZED") {
		t.Errorf("body does not contain error code: %s", body)
	}
}

func TestMiddlewareAllowsAnonymous(t *testing.T) {
	var called bool
	mw := identity.Middleware(identity.MiddlewareOptions{
		Identifier:     &stubIdentifier{name: "x", err: identity.ErrNoCredential},
		AllowAnonymous: true,
	})
	handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		if _, ok := identity.FromContext(r.Context()); ok {
			t.Error("anonymous path populated UserCtx")
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Error("handler was not called in anonymous mode")
	}
}

func TestMiddlewarePopulatesUserCtxOnSuccess(t *testing.T) {
	u := &identity.UserCtx{Subject: "sven", AuthMethod: identity.AuthMethodAPIKey}
	mw := identity.Middleware(identity.MiddlewareOptions{
		Identifier: &stubIdentifier{name: "x", uc: u},
	})
	var captured *identity.UserCtx
	handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured, _ = identity.FromContext(r.Context())
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if captured == nil || captured.Subject != "sven" {
		t.Fatalf("captured = %+v", captured)
	}
}

func TestMiddlewareRejectsInvalidCredential(t *testing.T) {
	mw := identity.Middleware(identity.MiddlewareOptions{
		Identifier: &stubIdentifier{name: "x", err: identity.ErrInvalidCredential},
	})
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run on invalid credential")
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Code = %d; want 401", w.Code)
	}
}

func TestMiddlewarePanicsWithoutIdentifier(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	identity.Middleware(identity.MiddlewareOptions{})
}

// sharedCtxKey is a package-level type so the stub Identifier and the
// downstream handler agree on how to look up a value placed by the
// outer test.
type sharedCtxKey struct{}

func TestMiddlewareContextPropagatesBetweenIdentifyAndHandler(t *testing.T) {
	u := &identity.UserCtx{Subject: "user-a"}
	parent := context.WithValue(context.Background(), sharedCtxKey{}, "parent-value")
	stub := &ctxCheckingStub{uc: u}
	mw := identity.Middleware(identity.MiddlewareOptions{Identifier: stub})
	handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if v, _ := r.Context().Value(sharedCtxKey{}).(string); v != "parent-value" {
			t.Error("downstream handler did not inherit parent ctx")
		}
	}))
	req := httptest.NewRequest("GET", "/", nil).WithContext(parent)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !stub.seenParent {
		t.Error("stub did not observe parent ctx")
	}
}

type ctxCheckingStub struct {
	uc         *identity.UserCtx
	seenParent bool
}

func (c *ctxCheckingStub) Name() string { return "ctxstub" }
func (c *ctxCheckingStub) Identify(ctx context.Context, _ *http.Request) (*identity.UserCtx, error) {
	if v, _ := ctx.Value(sharedCtxKey{}).(string); v == "parent-value" {
		c.seenParent = true
	}
	return c.uc, nil
}
