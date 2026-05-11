// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
)

// jwksServer is a tiny helper that serves a JWKS document from a set of
// public keys. fetchCount records the number of GETs it has seen so
// tests can assert the cache is working.
type jwksServer struct {
	*httptest.Server
	fetchCount atomic.Int32
	body       atomic.Pointer[[]byte]
}

func newJWKSServer(t *testing.T, keys ...any) *jwksServer {
	t.Helper()
	js := &jwksServer{}
	js.setKeys(t, keys...)
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.fetchCount.Add(1)
		body := js.body.Load()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(*body)
	}))
	t.Cleanup(js.Close)
	return js
}

func (s *jwksServer) setKeys(t *testing.T, keys ...any) {
	t.Helper()
	type jwk struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Alg string `json:"alg,omitempty"`
		Use string `json:"use,omitempty"`
		N   string `json:"n,omitempty"`
		E   string `json:"e,omitempty"`
		Crv string `json:"crv,omitempty"`
		X   string `json:"x,omitempty"`
		Y   string `json:"y,omitempty"`
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	for i, k := range keys {
		kid := fmt.Sprintf("key-%d", i)
		switch key := k.(type) {
		case *rsa.PublicKey:
			doc.Keys = append(doc.Keys, jwk{
				Kty: "RSA",
				Kid: kid,
				Alg: "RS256",
				Use: "sig",
				N:   base64URL(key.N.Bytes()),
				E:   base64URL(encodeBigInt(int64(key.E))),
			})
		case *ecdsa.PublicKey:
			x, y := ecCoords(key)
			doc.Keys = append(doc.Keys, jwk{
				Kty: "EC",
				Kid: kid,
				Alg: "ES256",
				Use: "sig",
				Crv: "P-256",
				X:   base64URL(x),
				Y:   base64URL(y),
			})
		default:
			t.Fatalf("unsupported key type %T", k)
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	s.body.Store(&body)
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// ecCoords extracts the X/Y coordinates from an ECDSA public key
// without relying on the deprecated big.Int field accessors directly.
// The Go 1.23 standard library still stores them as big.Int internally
// and the JWK encoding (RFC 7518) requires their byte form, so we fall
// back to the deprecated accessors behind a tiny wrapper to keep the
// deprecation warning localised.
func ecCoords(key *ecdsa.PublicKey) (x, y []byte) {
	//nolint:staticcheck // JWK encoding requires the raw coordinates.
	return key.X.Bytes(), key.Y.Bytes()
}

func encodeBigInt(n int64) []byte {
	// Minimal big-endian encoding, same as crypto/rsa.PublicKey marshal.
	switch {
	case n == 0:
		return []byte{0}
	case n < 256:
		return []byte{byte(n)}
	case n < 65536:
		return []byte{byte(n >> 8), byte(n)}
	default:
		return []byte{byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func TestJWKSClientCaches(t *testing.T) {
	pub := genRSAPub(t)
	srv := newJWKSServer(t, pub)

	now := time.Unix(1_700_000_000, 0).UTC()
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Hour,
		Clock:      func() time.Time { return now },
	})

	ctx := context.Background()
	for range 5 {
		key, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0)
		if err != nil {
			t.Fatalf("KeyFor: %v", err)
		}
		if key == nil {
			t.Fatal("nil key")
		}
	}
	if got := srv.fetchCount.Load(); got != 1 {
		t.Fatalf("fetchCount = %d; want 1 (cached)", got)
	}
}

func TestJWKSClientRefreshOnUnknownKid(t *testing.T) {
	pub1 := genRSAPub(t)
	srv := newJWKSServer(t, pub1)

	now := time.Unix(1_700_000_000, 0).UTC()
	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Hour,
		MinRefresh: 1 * time.Nanosecond,
		Clock:      func() time.Time { return now },
	})

	ctx := context.Background()
	// Prime the cache.
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Rotate keys on the server and request an unknown kid → should
	// trigger a refresh that sees the rotated set.
	pub2 := genRSAPub(t)
	srv.setKeys(t, pub1, pub2) // indices: key-0 (old), key-1 (new)

	key, _, err := client.KeyFor(ctx, srv.URL, "key-1", 0)
	if err != nil {
		t.Fatalf("KeyFor after rotate: %v", err)
	}
	if key == nil {
		t.Fatal("nil key after rotate")
	}
	if got := srv.fetchCount.Load(); got < 2 {
		t.Fatalf("fetchCount = %d; want >= 2 after rotate", got)
	}
}

func TestJWKSClientUnknownKidAfterRefresh(t *testing.T) {
	pub := genRSAPub(t)
	srv := newJWKSServer(t, pub)

	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Hour,
		MinRefresh: 1 * time.Nanosecond,
	})

	ctx := context.Background()
	_, _, err := client.KeyFor(ctx, srv.URL, "missing-kid", 0)
	if err == nil {
		t.Fatal("want err for unknown kid")
	}
	if !errors.Is(err, identity.ErrKeyNotFound) {
		t.Fatalf("want ErrKeyNotFound; got %v", err)
	}
}

func TestJWKSClientServesStaleOnFetchError(t *testing.T) {
	pub := genRSAPub(t)
	srv := newJWKSServer(t, pub)

	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: 1 * time.Nanosecond, // become stale immediately
	})

	ctx := context.Background()
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Kill the server; subsequent fetch fails but cache should still serve.
	srv.Close()
	key, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0)
	if err != nil {
		t.Fatalf("KeyFor after server close: %v", err)
	}
	if key == nil {
		t.Fatal("nil key from stale cache")
	}
}

func TestJWKSClientInvalidateForcesRefetch(t *testing.T) {
	pub := genRSAPub(t)
	srv := newJWKSServer(t, pub)

	client := identity.NewJWKSClient(identity.JWKSClientOptions{
		DefaultTTL: time.Hour,
	})

	ctx := context.Background()
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("prime: %v", err)
	}
	before := srv.fetchCount.Load()

	client.Invalidate(srv.URL)
	if _, _, err := client.KeyFor(ctx, srv.URL, "key-0", 0); err != nil {
		t.Fatalf("after invalidate: %v", err)
	}
	if srv.fetchCount.Load() <= before {
		t.Fatalf("fetch count unchanged after Invalidate (%d)", before)
	}
}

func TestJWKSClientMalformedPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	client := identity.NewJWKSClient(identity.JWKSClientOptions{})
	_, _, err := client.KeyFor(context.Background(), srv.URL, "kid", 0)
	if !errors.Is(err, identity.ErrJWKSMalformed) {
		t.Fatalf("want ErrJWKSMalformed; got %v", err)
	}
}

func genRSAPub(t *testing.T) *rsa.PublicKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa: %v", err)
	}
	return &key.PublicKey
}
