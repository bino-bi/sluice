// SPDX-License-Identifier: AGPL-3.0-or-later

package identity_test

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const testHMACSecret = "0123456789abcdef0123456789abcdef"

func TestHMACSecrets(t *testing.T) {
	t.Setenv("SLUICE_TEST_HMAC", testHMACSecret+"\n") // trailing newline must be trimmed

	bindings := []*apitypes.SubjectBinding{
		{Spec: apitypes.SubjectBindingSpec{Issuer: "iss1", HMACSecretRef: "secret://env/SLUICE_TEST_HMAC"}},
		{Spec: apitypes.SubjectBindingSpec{Issuer: "iss2"}}, // no ref → skipped
	}
	r := secrets.NewResolver(secrets.ResolverOptions{})

	got, err := identity.HMACSecrets(context.Background(), r, bindings)
	if err != nil {
		t.Fatalf("HMACSecrets: %v", err)
	}
	if string(got["iss1"]) != testHMACSecret {
		t.Errorf("iss1 secret = %q, want %q (trimmed)", got["iss1"], testHMACSecret)
	}
	if _, ok := got["iss2"]; ok {
		t.Errorf("iss2 has no hmacSecretRef; must be absent from the map")
	}
}

// A short or empty key lets anyone forge a token, and a binding without
// an issuer would store a secret that no lookup finds.
func TestHMACSecrets_Refuses(t *testing.T) {
	cases := []struct {
		name, value, issuer, want string
	}{
		{"empty", "\n", "iss1", "0 bytes"},
		{"short", strings.Repeat("a", 31), "iss1", "31 bytes"},
		{"no issuer", testHMACSecret, "", "needs an issuer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SLUICE_TEST_HMAC", tc.value)
			bindings := []*apitypes.SubjectBinding{{
				Metadata: apitypes.ObjectMeta{Name: "b1"},
				Spec:     apitypes.SubjectBindingSpec{Issuer: tc.issuer, HMACSecretRef: "secret://env/SLUICE_TEST_HMAC"},
			}}
			r := secrets.NewResolver(secrets.ResolverOptions{})
			_, err := identity.HMACSecrets(context.Background(), r, bindings)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one with %q", err, tc.want)
			}
		})
	}
}

// TestAPIKeyBindings exercises the SubjectBinding → identity translation. The SubjectBinding.Claims block carries
// literal values; the per-key hashRef resolves through the env provider;
// the resulting identity.APIKeyBinding must carry the decoded hash plus
// tenantId in UserCtx-bound claims.
func TestAPIKeyBindings(t *testing.T) {
	pepper := []byte("test-pepper")
	want := identity.ComputeHash(pepper, "hello", "world")
	hexHash := hex.EncodeToString(want)

	t.Setenv("SLUICE_TEST_APIKEY_HASH", hexHash)

	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	bindings := []*apitypes.SubjectBinding{{
		Metadata: apitypes.ObjectMeta{Name: "demo-apikeys"},
		Spec: apitypes.SubjectBindingSpec{
			Claims: apitypes.ClaimPaths{
				SubjectID: "hello",
				TenantID:  "acme",
			},
			APIKeys: []apitypes.APIKeyBinding{{
				ID:      "hello",
				HashRef: "secret://env/SLUICE_TEST_APIKEY_HASH",
				Groups:  []string{"analytics"},
			}},
		},
	}}

	got, err := identity.APIKeyBindings(context.Background(), resolver, bindings, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("APIKeyBindings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d bindings; want 1", len(got))
	}
	b := got[0]
	if b.ID != "hello" {
		t.Errorf("ID = %q; want hello", b.ID)
	}
	if hex.EncodeToString(b.Hash) != hexHash {
		t.Errorf("Hash mismatch: got %s; want %s", hex.EncodeToString(b.Hash), hexHash)
	}
	if b.Subject != "hello" {
		t.Errorf("Subject = %q; want hello", b.Subject)
	}
	if b.Issuer != "demo-apikeys" {
		t.Errorf("Issuer = %q; want demo-apikeys", b.Issuer)
	}
	if len(b.Groups) != 1 || b.Groups[0] != "analytics" {
		t.Errorf("Groups = %v; want [analytics]", b.Groups)
	}
	if b.Claims["tenantId"] != "acme" {
		t.Errorf("Claims[tenantId] = %v; want acme", b.Claims["tenantId"])
	}
}

// TestAPIKeyBindings_SkipsBadRef exercises the "one rotated-out key
// should not blank the table" invariant: a bindings list with one good
// and one broken entry returns the good entry and logs a warning for
// the bad one, without surfacing an error.
func TestAPIKeyBindings_SkipsBadRef(t *testing.T) {
	pepper := []byte("p")
	good := hex.EncodeToString(identity.ComputeHash(pepper, "good", "m"))
	t.Setenv("SLUICE_TEST_APIKEY_GOOD", good)

	// Write a file with non-hex content; resolver reads it, DecodeHash rejects it.
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.hash")
	if err := os.WriteFile(badFile, []byte("not-hex-gibberish"), 0o600); err != nil {
		t.Fatalf("write bad hash file: %v", err)
	}

	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	bindings := []*apitypes.SubjectBinding{{
		Metadata: apitypes.ObjectMeta{Name: "b"},
		Spec: apitypes.SubjectBindingSpec{
			APIKeys: []apitypes.APIKeyBinding{
				{ID: "good", HashRef: "secret://env/SLUICE_TEST_APIKEY_GOOD"},
				{ID: "bad", HashRef: "secret://file/" + badFile},
			},
		},
	}}
	got, err := identity.APIKeyBindings(context.Background(), resolver, bindings, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("want single 'good' binding; got %+v", got)
	}
}
