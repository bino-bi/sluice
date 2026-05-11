// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

// stubIdentifier returns a canned (UserCtx, error). Used to exercise
// Composite without wiring a full JWT or API-key flow.
type stubIdentifier struct {
	name string
	uc   *identity.UserCtx
	err  error
}

func (s *stubIdentifier) Name() string { return s.name }
func (s *stubIdentifier) Identify(_ context.Context, _ *http.Request) (*identity.UserCtx, error) {
	return s.uc, s.err
}

func TestCompositeReturnsFirstSuccess(t *testing.T) {
	u := &identity.UserCtx{Subject: "sven"}
	c := identity.NewComposite(
		&stubIdentifier{name: "first", err: identity.ErrNoCredential},
		&stubIdentifier{name: "second", uc: u},
	)
	got, err := c.Identify(context.Background(), httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Subject != "sven" {
		t.Errorf("Subject = %q", got.Subject)
	}
}

func TestCompositePropagatesFirstInvalid(t *testing.T) {
	c := identity.NewComposite(
		&stubIdentifier{name: "bad_bearer", err: identity.ErrInvalidCredential},
		&stubIdentifier{name: "no_api_key", err: identity.ErrNoCredential},
	)
	_, err := c.Identify(context.Background(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(err, identity.ErrInvalidCredential) {
		t.Fatalf("err = %v; want ErrInvalidCredential", err)
	}
}

func TestCompositeReturnsNoCredentialWhenAllMiss(t *testing.T) {
	c := identity.NewComposite(
		&stubIdentifier{name: "a", err: identity.ErrNoCredential},
		&stubIdentifier{name: "b", err: identity.ErrNoCredential},
	)
	_, err := c.Identify(context.Background(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("err = %v; want ErrNoCredential", err)
	}
}

func TestCompositeEmpty(t *testing.T) {
	c := identity.NewComposite()
	_, err := c.Identify(context.Background(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("empty composite must return ErrNoCredential; got %v", err)
	}
}

func TestCompositeSkipsNilChildren(t *testing.T) {
	u := &identity.UserCtx{Subject: "x"}
	c := identity.NewComposite(
		nil,
		&stubIdentifier{name: "real", uc: u},
	)
	got, err := c.Identify(context.Background(), httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "x" {
		t.Errorf("Subject = %q", got.Subject)
	}
}

func TestCompositeMethods(t *testing.T) {
	c := identity.NewComposite(
		&stubIdentifier{name: "a", err: identity.ErrNoCredential},
		&stubIdentifier{name: "b", err: identity.ErrNoCredential},
	)
	got := c.Methods()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Methods = %v", got)
	}
}
