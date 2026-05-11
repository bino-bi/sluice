// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
)

func apiKeyFixture(pepper []byte, keyID, material string, groups ...string) (id *identity.APIKeyIdentifier, full string) {
	hash := identity.ComputeHash(pepper, keyID, material)
	id = identity.NewAPIKeyIdentifier(identity.APIKeyOptions{
		Pepper: pepper,
		Bindings: []identity.APIKeyBinding{
			{
				ID:      keyID,
				Hash:    hash,
				Subject: "user-" + keyID,
				Issuer:  "test-suite",
				Groups:  groups,
			},
		},
		Clock: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	full = keyID + "." + material
	return id, full
}

func TestAPIKeyMissingHeaderReturnsNoCredential(t *testing.T) {
	id, _ := apiKeyFixture([]byte("pepper"), "sl_dev_k1", "abcdef")
	r := httptest.NewRequest("GET", "/", nil)
	_, err := id.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("err = %v; want ErrNoCredential", err)
	}
}

func TestAPIKeyValidKeyAuthorizes(t *testing.T) {
	id, key := apiKeyFixture([]byte("pepper"), "sl_dev_k1", "abcdef123", "admins")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Api-Key", key)

	uc, err := id.Identify(context.Background(), r)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if uc.Subject != "user-sl_dev_k1" {
		t.Errorf("Subject = %q", uc.Subject)
	}
	if uc.AuthMethod != identity.AuthMethodAPIKey {
		t.Errorf("AuthMethod = %q", uc.AuthMethod)
	}
	if !uc.HasGroup("admins") {
		t.Errorf("Groups = %v; want admins", uc.Groups)
	}
	if uc.Issuer != "test-suite" {
		t.Errorf("Issuer = %q", uc.Issuer)
	}
}

func TestAPIKeyAcceptsAuthorizationHeaderForm(t *testing.T) {
	id, key := apiKeyFixture([]byte("pepper"), "sl_dev_k1", "abcdef")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "ApiKey "+key)

	if _, err := id.Identify(context.Background(), r); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestAPIKeyBearerIsIgnored(t *testing.T) {
	// Bearer tokens are for the JWT identifier; the API key identifier
	// must treat a Bearer header as "no credential".
	id := identity.NewAPIKeyIdentifier(identity.APIKeyOptions{
		Pepper: []byte("pepper"),
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer eyJ...")
	if _, err := id.Identify(context.Background(), r); !errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("err = %v; want ErrNoCredential", err)
	}
}

func TestAPIKeyUnknownKeyIDIsInvalid(t *testing.T) {
	id, _ := apiKeyFixture([]byte("pepper"), "sl_dev_k1", "abcdef")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Api-Key", "sl_dev_unknown.material")

	_, err := id.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrInvalidCredential) {
		t.Fatalf("err = %v; want ErrInvalidCredential", err)
	}
}

func TestAPIKeyBadHMACIsInvalid(t *testing.T) {
	id, _ := apiKeyFixture([]byte("pepper"), "sl_dev_k1", "abcdef")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Api-Key", "sl_dev_k1.wrong-material")

	_, err := id.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrInvalidCredential) {
		t.Fatalf("err = %v; want ErrInvalidCredential", err)
	}
}

func TestAPIKeyMalformedReturnsInvalid(t *testing.T) {
	id := identity.NewAPIKeyIdentifier(identity.APIKeyOptions{Pepper: []byte("p")})
	for _, raw := range []string{"no-dot", ".only-material", "only-id.", ""} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Api-Key", raw)
		_, err := id.Identify(context.Background(), r)
		if raw == "" && !errors.Is(err, identity.ErrNoCredential) {
			t.Errorf("empty header must yield ErrNoCredential; got %v", err)
			continue
		}
		if raw != "" && !errors.Is(err, identity.ErrInvalidCredential) {
			t.Errorf("raw %q: err = %v; want ErrInvalidCredential", raw, err)
		}
	}
}

func TestComputeHashDeterministic(t *testing.T) {
	a := identity.ComputeHash([]byte("pep"), "id1", "mat")
	b := identity.ComputeHash([]byte("pep"), "id1", "mat")
	if !bytes.Equal(a, b) {
		t.Error("ComputeHash not deterministic")
	}
	c := identity.ComputeHash([]byte("pep2"), "id1", "mat")
	if bytes.Equal(a, c) {
		t.Error("ComputeHash insensitive to pepper")
	}
}

func TestDecodeHash(t *testing.T) {
	h := identity.ComputeHash([]byte("p"), "id", "m")
	hexEncoded := hex.EncodeToString(h)
	decoded, err := identity.DecodeHash(hexEncoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, h) {
		t.Error("DecodeHash round-trip mismatch")
	}
	if _, err := identity.DecodeHash("not-hex"); err == nil {
		t.Error("expected error on non-hex")
	}
	if _, err := identity.DecodeHash("deadbeef"); err == nil {
		t.Error("expected error on short hash")
	}
}

func TestAPIKeyNameIsApiKey(t *testing.T) {
	id := identity.NewAPIKeyIdentifier(identity.APIKeyOptions{})
	if id.Name() != "api_key" {
		t.Errorf("Name = %q", id.Name())
	}
}

func TestAPIKeyIdentifyPopulatesClaims(t *testing.T) {
	pepper := []byte("pepper")
	hash := identity.ComputeHash(pepper, "k1", "mat")
	id := identity.NewAPIKeyIdentifier(identity.APIKeyOptions{
		Pepper: pepper,
		Bindings: []identity.APIKeyBinding{{
			ID:     "k1",
			Hash:   hash,
			Claims: map[string]any{"tenantId": "acme", "region": "eu-1"},
		}},
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Api-Key", "k1.mat")
	uc, err := id.Identify(context.Background(), r)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := uc.Claims["tenantId"]; got != "acme" {
		t.Errorf("Claims[tenantId] = %v; want acme", got)
	}
	if got := uc.Claims["region"]; got != "eu-1" {
		t.Errorf("Claims[region] = %v; want eu-1", got)
	}
}

func TestAPIKeySetBindingsAtomicSwap(t *testing.T) {
	pepper := []byte("pepper")
	oldHash := identity.ComputeHash(pepper, "old", "mat")
	id := identity.NewAPIKeyIdentifier(identity.APIKeyOptions{
		Pepper: pepper,
		Bindings: []identity.APIKeyBinding{
			{ID: "old", Hash: oldHash},
		},
	})

	// Baseline: old key authenticates.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Api-Key", "old.mat")
	if _, err := id.Identify(context.Background(), r); err != nil {
		t.Fatalf("pre-swap identify: %v", err)
	}

	newHash := identity.ComputeHash(pepper, "new", "secret")
	id.SetBindings([]identity.APIKeyBinding{
		{ID: "new", Hash: newHash},
	})

	// Old key must now miss; new key must authenticate.
	if _, err := id.Identify(context.Background(), httptest.NewRequest("GET", "/", nil)); !errors.Is(err, identity.ErrNoCredential) {
		t.Errorf("post-swap, no header: err = %v", err)
	}
	old := httptest.NewRequest("GET", "/", nil)
	old.Header.Set("X-Api-Key", "old.mat")
	if _, err := id.Identify(context.Background(), old); !errors.Is(err, identity.ErrInvalidCredential) {
		t.Errorf("post-swap, old key should no longer be known: err = %v", err)
	}
	fresh := httptest.NewRequest("GET", "/", nil)
	fresh.Header.Set("X-Api-Key", "new.secret")
	if _, err := id.Identify(context.Background(), fresh); err != nil {
		t.Errorf("post-swap, new key should authenticate: err = %v", err)
	}
}
