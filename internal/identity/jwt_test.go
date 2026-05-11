// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

type jwtFixture struct {
	priv     *rsa.PrivateKey
	jwksURL  string
	bindings *identity.BindingRegistry
	verifier *identity.JWTIdentifier
	now      time.Time
}

func newJWTFixture(t *testing.T) *jwtFixture {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa: %v", err)
	}

	js := newJWKSServer(t, &priv.PublicKey)
	binding := apitypes.SubjectBinding{
		Metadata: apitypes.ObjectMeta{Name: "default"},
		Spec: apitypes.SubjectBindingSpec{
			Issuer:   "https://issuer.example",
			Audience: "sluice-api",
			JWKSURL:  js.URL,
			Claims: apitypes.ClaimPaths{
				SubjectID: "sub",
				Email:     "email",
				Groups:    "groups",
			},
		},
	}
	reg, err := identity.NewBindingRegistry([]apitypes.SubjectBinding{binding})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()

	verifier, err := identity.NewJWTIdentifier(identity.JWTOptions{
		Bindings: reg,
		Clock:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	return &jwtFixture{
		priv:     priv,
		jwksURL:  js.URL,
		bindings: reg,
		verifier: verifier,
		now:      now,
	}
}

func (f *jwtFixture) mint(t *testing.T, overrides func(*jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":    "https://issuer.example",
		"aud":    "sluice-api",
		"sub":    "alice",
		"email":  "alice@example.com",
		"groups": []string{"admins", "editors"},
		"iat":    f.now.Unix(),
		"exp":    f.now.Add(10 * time.Minute).Unix(),
	}
	if overrides != nil {
		overrides(&claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "key-0"
	raw, err := tok.SignedString(f.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

func TestJWTValidToken(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, nil)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	uc, err := f.verifier.Identify(context.Background(), r)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if uc.Subject != "alice" {
		t.Fatalf("subject = %q; want alice", uc.Subject)
	}
	if uc.Email != "alice@example.com" {
		t.Fatalf("email = %q", uc.Email)
	}
	if uc.Issuer != "https://issuer.example" {
		t.Fatalf("issuer = %q", uc.Issuer)
	}
	if !uc.HasGroup("admins") {
		t.Fatalf("groups = %v; want admins present", uc.Groups)
	}
	if uc.AuthMethod != identity.AuthMethodJWT {
		t.Fatalf("authMethod = %s", uc.AuthMethod)
	}
}

func TestJWTMissingBearer(t *testing.T) {
	f := newJWTFixture(t)
	r := httptest.NewRequest("GET", "/", nil)
	_, err := f.verifier.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("err = %v; want ErrNoCredential", err)
	}
}

func TestJWTExpired(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, func(c *jwt.MapClaims) {
		(*c)["exp"] = f.now.Add(-1 * time.Hour).Unix()
		(*c)["iat"] = f.now.Add(-2 * time.Hour).Unix()
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	_, err := f.verifier.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrJWTExpired) {
		t.Fatalf("err = %v; want ErrJWTExpired", err)
	}
}

func TestJWTNotYetValid(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, func(c *jwt.MapClaims) {
		(*c)["nbf"] = f.now.Add(2 * time.Hour).Unix()
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	_, err := f.verifier.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrJWTNotYetValid) {
		t.Fatalf("err = %v; want ErrJWTNotYetValid", err)
	}
}

func TestJWTWrongAudience(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, func(c *jwt.MapClaims) {
		(*c)["aud"] = "some-other-api"
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	_, err := f.verifier.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrJWTWrongAudience) {
		t.Fatalf("err = %v; want ErrJWTWrongAudience", err)
	}
}

func TestJWTUnknownIssuer(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, func(c *jwt.MapClaims) {
		(*c)["iss"] = "https://evil.example"
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	_, err := f.verifier.Identify(context.Background(), r)
	if !errors.Is(err, identity.ErrJWTUnknownIssuer) {
		t.Fatalf("err = %v; want ErrJWTUnknownIssuer", err)
	}
}

func TestJWTBadSignature(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, nil)
	// Replace the signature with a signature from a different token
	// (same header/payload structure, different material). This gives
	// us a deterministically-verifiable-but-invalid signature.
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token shape: %s", raw)
	}
	// Flip bits across the whole signature so no chance of it
	// accidentally verifying.
	sig := []byte(parts[2])
	for i := range sig {
		sig[i] ^= 0x5A
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tampered)
	_, err := f.verifier.Identify(context.Background(), r)
	if err == nil {
		t.Fatal("want signature verification error")
	}
	// Accept either malformed (if the mutation produced invalid base64)
	// or bad-signature (if it parsed but failed to verify).
	if !errors.Is(err, identity.ErrJWTBadSignature) && !errors.Is(err, identity.ErrJWTMalformed) && !errors.Is(err, identity.ErrInvalidCredential) {
		t.Fatalf("err = %v; want credential error", err)
	}
}

func TestJWTAlgNoneRejected(t *testing.T) {
	f := newJWTFixture(t)
	// Mint an alg=none token manually since the library's built-in
	// SigningMethodNone returns "none" which is NOT on the allowlist.
	claims := jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "sluice-api",
		"sub": "alice",
		"iat": f.now.Unix(),
		"exp": f.now.Add(10 * time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	raw, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	_, err = f.verifier.Identify(context.Background(), r)
	if err == nil {
		t.Fatal("want alg=none to be rejected")
	}
}

func TestJWTHMACPath(t *testing.T) {
	secret := []byte("super-secret-hmac-value-32-bytes!")

	binding := apitypes.SubjectBinding{
		Metadata: apitypes.ObjectMeta{Name: "hmac"},
		Spec: apitypes.SubjectBindingSpec{
			Issuer:   "https://hmac-issuer.example",
			Audience: "sluice-hmac",
			Claims:   apitypes.ClaimPaths{SubjectID: "sub"},
		},
	}
	reg, err := identity.NewBindingRegistry([]apitypes.SubjectBinding{binding})
	if err != nil {
		t.Fatalf("reg: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	v, err := identity.NewJWTIdentifier(identity.JWTOptions{
		Bindings:    reg,
		HMACSecrets: map[string][]byte{"https://hmac-issuer.example": secret},
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	claims := jwt.MapClaims{
		"iss": "https://hmac-issuer.example",
		"aud": "sluice-hmac",
		"sub": "bob",
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = "hmac-1"
	raw, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	uc, err := v.Identify(context.Background(), r)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if uc.Subject != "bob" {
		t.Fatalf("subject = %q", uc.Subject)
	}
}

func TestJWTAudienceArrayClaim(t *testing.T) {
	f := newJWTFixture(t)
	raw := f.mint(t, func(c *jwt.MapClaims) {
		(*c)["aud"] = []string{"some-other", "sluice-api", "third"}
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	if _, err := f.verifier.Identify(context.Background(), r); err != nil {
		t.Fatalf("Identify: %v", err)
	}
}

func TestJWTBindingRegistryReload(t *testing.T) {
	reg, err := identity.NewBindingRegistry(nil)
	if err != nil {
		t.Fatalf("reg: %v", err)
	}
	if _, ok := reg.ForIssuer("iss-a"); ok {
		t.Fatal("empty registry should not match")
	}
	err = reg.Apply([]apitypes.SubjectBinding{
		{Metadata: apitypes.ObjectMeta{Name: "a"}, Spec: apitypes.SubjectBindingSpec{Issuer: "iss-a"}},
		{Metadata: apitypes.ObjectMeta{Name: "b"}, Spec: apitypes.SubjectBindingSpec{Issuer: "iss-b"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := reg.ForIssuer("iss-a"); !ok {
		t.Fatal("iss-a missing post-apply")
	}
	if _, ok := reg.ForIssuer("iss-b"); !ok {
		t.Fatal("iss-b missing post-apply")
	}
	err = reg.Apply([]apitypes.SubjectBinding{
		{Metadata: apitypes.ObjectMeta{Name: "a"}, Spec: apitypes.SubjectBindingSpec{Issuer: "iss-a"}},
		{Metadata: apitypes.ObjectMeta{Name: "a2"}, Spec: apitypes.SubjectBindingSpec{Issuer: "iss-a"}},
	})
	if err == nil {
		t.Fatal("duplicate issuer should error")
	}
}
