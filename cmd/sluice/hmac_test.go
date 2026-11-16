// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const testHMACSecret = "0123456789abcdef0123456789abcdef"

func TestBuildHMACSecrets(t *testing.T) {
	t.Setenv("SLUICE_TEST_HMAC", testHMACSecret+"\n") // trailing newline must be trimmed

	snap := &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{
			{Spec: apitypes.SubjectBindingSpec{Issuer: "iss1", HMACSecretRef: "secret://env/SLUICE_TEST_HMAC"}},
			{Spec: apitypes.SubjectBindingSpec{Issuer: "iss2"}}, // no ref → skipped
		},
	}
	r := secrets.NewResolver(secrets.ResolverOptions{})

	got, err := buildHMACSecrets(context.Background(), r, snap)
	if err != nil {
		t.Fatalf("buildHMACSecrets: %v", err)
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
func TestBuildHMACSecrets_Refuses(t *testing.T) {
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
			snap := &config.Snapshot{
				SubjectBindings: []*apitypes.SubjectBinding{{
					Metadata: apitypes.ObjectMeta{Name: "b1"},
					Spec:     apitypes.SubjectBindingSpec{Issuer: tc.issuer, HMACSecretRef: "secret://env/SLUICE_TEST_HMAC"},
				}},
			}
			r := secrets.NewResolver(secrets.ResolverOptions{})
			_, err := buildHMACSecrets(context.Background(), r, snap)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one with %q", err, tc.want)
			}
		})
	}
}
