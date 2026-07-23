// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestBuildIdentity_NoBindings_ConstructsJWT locks in the reload
// precondition: the JWT identifier and binding registry exist even when
// the boot snapshot has zero SubjectBindings, so a later reload can
// introduce the first issuer without a restart. A Bearer token presented
// against the empty registry is rejected (fail-closed), not silently
// treated as anonymous.
func TestBuildIdentity_NoBindings_ConstructsJWT(t *testing.T) {
	scfg := config.DefaultServerConfig()
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})

	stack, err := buildIdentity(context.Background(), &scfg, resolver, &config.Snapshot{}, discardLogger())
	if err != nil {
		t.Fatalf("buildIdentity: %v", err)
	}
	if stack.jwt == nil || stack.bindings == nil {
		t.Fatal("jwt identifier and binding registry must exist with zero bindings")
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer not-a-valid-token")
	_, err = stack.identifier.Identify(context.Background(), r)
	if err == nil || errors.Is(err, identity.ErrNoCredential) {
		t.Fatalf("a Bearer token against zero bindings must be rejected as invalid, got: %v", err)
	}
}

// TestApplyReload_JWTBindings drives the reload subscriber body against a
// minimal runtimeDeps and proves a snapshot edit re-wires issuer set and
// HMAC secrets without a restart, while a bad snapshot keeps the previous
// state.
func TestApplyReload_JWTBindings(t *testing.T) {
	t.Setenv("SLUICE_TEST_RELOAD_HMAC", "reload-secret-32-bytes-padded!!!!")

	scfg := config.DefaultServerConfig()
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})
	stack, err := buildIdentity(context.Background(), &scfg, resolver, &config.Snapshot{}, discardLogger())
	if err != nil {
		t.Fatalf("buildIdentity: %v", err)
	}
	deps := &runtimeDeps{
		log:         discardLogger(),
		resolver:    resolver,
		jwtID:       stack.jwt,
		jwtBindings: stack.bindings,
	}

	issuer := "https://reload.example"
	binding := &apitypes.SubjectBinding{
		Metadata: apitypes.ObjectMeta{Name: "reload"},
		Spec: apitypes.SubjectBindingSpec{
			Issuer:        issuer,
			Audience:      "sluice-api",
			Claims:        apitypes.ClaimPaths{SubjectID: "sub"},
			HMACSecretRef: "secret://env/SLUICE_TEST_RELOAD_HMAC",
		},
	}
	deps.applyReload(context.Background(), &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{binding},
	})

	if _, ok := stack.bindings.ForIssuer(issuer); !ok {
		t.Fatal("issuer must be resolvable after reload")
	}

	// A token signed with the reloaded HMAC secret authenticates end to
	// end — proving SetHMACSecrets was applied, not just the registry.
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": issuer,
		"aud": "sluice-api",
		"sub": "carol",
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	})
	raw, err := tok.SignedString([]byte("reload-secret-32-bytes-padded!!!!"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+raw)
	uc, err := stack.identifier.Identify(context.Background(), r)
	if err != nil {
		t.Fatalf("Identify after reload: %v", err)
	}
	if uc.Subject != "carol" {
		t.Fatalf("subject = %q, want carol", uc.Subject)
	}

	// A snapshot with duplicate issuers is rejected; the previous binding
	// set stays live.
	deps.applyReload(context.Background(), &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{
			binding,
			{Metadata: apitypes.ObjectMeta{Name: "dup"}, Spec: apitypes.SubjectBindingSpec{Issuer: issuer}},
		},
	})
	if _, ok := stack.bindings.ForIssuer(issuer); !ok {
		t.Fatal("previous bindings must survive a rejected reload")
	}
	// A valid empty snapshot drops the issuer.
	deps.applyReload(context.Background(), &config.Snapshot{})
	if _, ok := stack.bindings.ForIssuer(issuer); ok {
		t.Fatal("issuer must be gone after an empty-snapshot reload")
	}
}

func TestBuildApprovalBroker_BaseURLOnly(t *testing.T) {
	scfg := config.DefaultServerConfig()
	scfg.Approval.PublicBaseURL = "https://sluice.example"
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})

	b, err := buildApprovalBroker(context.Background(), &scfg, &config.Snapshot{}, resolver, nil, discardLogger())
	if err != nil {
		t.Fatalf("buildApprovalBroker: %v", err)
	}
	if b == nil {
		t.Fatal("broker must be built when publicBaseUrl is set, so reload-added policies work")
	}
}

func TestBuildApprovalBroker_PoliciesWithoutBaseURL(t *testing.T) {
	scfg := config.DefaultServerConfig()
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})
	snap := &config.Snapshot{ByKind: map[apitypes.Kind][]apitypes.Object{
		apitypes.KindApprovalPolicy: {&apitypes.ApprovalPolicy{}},
	}}

	_, err := buildApprovalBroker(context.Background(), &scfg, snap, resolver, nil, discardLogger())
	if err == nil {
		t.Fatal("policies without approval.publicBaseUrl must fail startup")
	}
}
