// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
)

func TestWithUserAndFromContext(t *testing.T) {
	u := &identity.UserCtx{
		Subject:    "sven",
		AuthMethod: identity.AuthMethodAPIKey,
		AuthTime:   time.Now(),
		Groups:     []string{"admins"},
	}
	ctx := identity.WithUser(context.Background(), u)
	got, ok := identity.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext ok=false")
	}
	if got.Subject != "sven" {
		t.Errorf("Subject = %q; want sven", got.Subject)
	}
}

func TestFromContextEmpty(t *testing.T) {
	if _, ok := identity.FromContext(context.Background()); ok {
		t.Error("FromContext(background) returned ok=true")
	}
}

func TestWithUserNilRemoves(t *testing.T) {
	ctx := identity.WithUser(context.Background(), &identity.UserCtx{Subject: "a"})
	ctx = identity.WithUser(ctx, nil)
	if _, ok := identity.FromContext(ctx); ok {
		t.Error("expected FromContext ok=false after WithUser(nil)")
	}
}

func TestMustFromContextPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = identity.MustFromContext(context.Background())
}

func TestHasGroup(t *testing.T) {
	u := &identity.UserCtx{Groups: []string{"admins", "readers"}}
	if !u.HasGroup("admins") {
		t.Error("HasGroup(admins) = false")
	}
	if u.HasGroup("ghost") {
		t.Error("HasGroup(ghost) = true")
	}
	var nilU *identity.UserCtx
	if nilU.HasGroup("x") {
		t.Error("nil receiver must return false")
	}
}

func TestCloneIsDeep(t *testing.T) {
	orig := &identity.UserCtx{
		Subject: "sven",
		Groups:  []string{"admins"},
		Claims:  map[string]any{"tenant": "a"},
	}
	c := orig.Clone()
	c.Groups[0] = "hackers"
	c.Claims["tenant"] = "b"
	if orig.Groups[0] != "admins" {
		t.Error("Clone did not deep-copy Groups")
	}
	if orig.Claims["tenant"] != "a" {
		t.Error("Clone did not deep-copy Claims")
	}
}

func TestCloneNil(t *testing.T) {
	var u *identity.UserCtx
	if u.Clone() != nil {
		t.Error("Clone on nil must return nil")
	}
}
