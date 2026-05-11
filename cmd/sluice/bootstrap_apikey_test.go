// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestBuildAPIKeyBindings exercises the snapshot → identity translation
// the bootstrap relies on. The SubjectBinding.Claims block carries
// literal values; the per-key hashRef resolves through the env provider;
// the resulting identity.APIKeyBinding must carry the decoded hash plus
// tenantId in UserCtx-bound claims.
func TestBuildAPIKeyBindings(t *testing.T) {
	pepper := []byte("test-pepper")
	want := identity.ComputeHash(pepper, "hello", "world")
	hexHash := hex.EncodeToString(want)

	t.Setenv("SLUICE_TEST_APIKEY_HASH", hexHash)

	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	snap := &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{{
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
		}},
	}

	got, err := buildAPIKeyBindings(context.Background(), resolver, snap, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildAPIKeyBindings: %v", err)
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

// TestBuildAPIKeyBindings_SkipsBadRef exercises the "one rotated-out key
// should not blank the table" invariant — a bindings list with one good
// and one broken entry returns the good entry and logs a warning for
// the bad one, without surfacing an error.
func TestBuildAPIKeyBindings_SkipsBadRef(t *testing.T) {
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
	snap := &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{{
			Metadata: apitypes.ObjectMeta{Name: "b"},
			Spec: apitypes.SubjectBindingSpec{
				APIKeys: []apitypes.APIKeyBinding{
					{ID: "good", HashRef: "secret://env/SLUICE_TEST_APIKEY_GOOD"},
					{ID: "bad", HashRef: "secret://file/" + badFile},
				},
			},
		}},
	}
	got, err := buildAPIKeyBindings(context.Background(), resolver, snap, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("want single 'good' binding; got %+v", got)
	}
}
