// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestBuildHMACSecrets(t *testing.T) {
	t.Setenv("SLUICE_TEST_HMAC", "supersecret\n") // trailing newline must be trimmed

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
	if string(got["iss1"]) != "supersecret" {
		t.Errorf("iss1 secret = %q, want %q (trimmed)", got["iss1"], "supersecret")
	}
	if _, ok := got["iss2"]; ok {
		t.Errorf("iss2 has no hmacSecretRef; must be absent from the map")
	}
}
