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
	"github.com/bino-bi/sluice/internal/policy"
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

// reload runs the prepare and, when it succeeds, the apply of a reload.
func reload(deps *runtimeDeps, snap *config.Snapshot) error {
	prep, err := deps.prepareReload(context.Background(), snap)
	if err != nil {
		return err
	}
	prep.Apply()
	return nil
}

// TestApplyReload_JWTBindings drives the reload body against a minimal
// runtimeDeps and proves a snapshot edit re-wires issuer set and HMAC
// secrets without a restart, while a bad snapshot keeps the previous
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
	if err := reload(deps, &config.Snapshot{SubjectBindings: []*apitypes.SubjectBinding{binding}}); err != nil {
		t.Fatalf("reload: %v", err)
	}

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
	err = reload(deps, &config.Snapshot{
		SubjectBindings: []*apitypes.SubjectBinding{
			binding,
			{Metadata: apitypes.ObjectMeta{Name: "dup"}, Spec: apitypes.SubjectBindingSpec{Issuer: issuer}},
		},
	})
	if err == nil {
		t.Fatal("a duplicate issuer must fail the reload")
	}
	if _, ok := stack.bindings.ForIssuer(issuer); !ok {
		t.Fatal("previous bindings must survive a rejected reload")
	}
	// A valid empty snapshot drops the issuer.
	if err := reload(deps, &config.Snapshot{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := stack.bindings.ForIssuer(issuer); ok {
		t.Fatal("issuer must be gone after an empty-snapshot reload")
	}
}

func hmacBinding(name, issuer, ref string) *apitypes.SubjectBinding {
	return &apitypes.SubjectBinding{
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec: apitypes.SubjectBindingSpec{
			Issuer:        issuer,
			Audience:      "sluice-api",
			Claims:        apitypes.ClaimPaths{SubjectID: "sub"},
			HMACSecretRef: ref,
		},
	}
}

func hs256(t *testing.T, issuer, secret string) string {
	t.Helper()
	now := time.Now()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": issuer, "aud": "sluice-api", "sub": "carol",
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// One change removes a compromised issuer and adds a binding whose
// hmacSecretRef cannot be resolved. The reload fails as a whole: the
// error reaches the caller, config_version stays, and every part keeps
// the old snapshot, also the removed issuer and the policies. Before,
// policies and limits moved on, the old JWT trust stayed, and the reload
// reported success.
func TestReload_FailedPartKeepsTheOldState(t *testing.T) {
	const secretA, secretB = "secret-a-32-bytes-padded-!!!!!!!!", "secret-b-32-bytes-padded-!!!!!!!!"
	t.Setenv("SLUICE_TEST_HMAC_A", secretA)
	t.Setenv("SLUICE_TEST_HMAC_B", secretB)
	ctx := context.Background()

	scfg := config.DefaultServerConfig()
	resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})
	boot := &config.Snapshot{Digest: "boot", SubjectBindings: []*apitypes.SubjectBinding{
		hmacBinding("a", "https://a.example", "secret://env/SLUICE_TEST_HMAC_A"),
		hmacBinding("b", "https://b.example", "secret://env/SLUICE_TEST_HMAC_B"),
	}}
	stack, err := buildIdentity(ctx, &scfg, resolver, boot, discardLogger())
	if err != nil {
		t.Fatalf("buildIdentity: %v", err)
	}
	eng := policy.New(policy.Options{Logger: discardLogger()})
	reg := config.NewRegistry()
	reg.Publish(boot)
	if err := eng.ApplySnapshot(ctx, boot); err != nil {
		t.Fatal(err)
	}
	deps := &runtimeDeps{
		log: discardLogger(), resolver: resolver, registry: reg, policyEngine: eng,
		identifier: stack.identifier, jwtID: stack.jwt, jwtBindings: stack.bindings,
	}

	next := &config.Snapshot{Digest: "next", SubjectBindings: []*apitypes.SubjectBinding{
		hmacBinding("a", "https://a.example", "secret://env/SLUICE_TEST_HMAC_A"),
		hmacBinding("c", "https://c.example", "secret://env/SLUICE_TEST_HMAC_MISSING"),
	}}
	w, err := config.NewWatcher(config.WatchOptions{
		Dir:      t.TempDir(),
		Registry: reg,
		Logger:   discardLogger(),
		Load:     func(context.Context, config.LoadOptions) (*config.Snapshot, error) { return next, nil },
		Validate: deps.prepareReload,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Reload(ctx); err == nil {
		t.Fatal("a reload with an unresolvable hmacSecretRef must fail")
	}
	if got := reg.Current().Version; got != 1 {
		t.Errorf("config_version = %d, want it to stay 1", got)
	}
	if got := eng.Snapshot().Digest; got != "boot" {
		t.Errorf("policy digest = %q, want boot", got)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+hs256(t, "https://b.example", secretB))
	if _, err := stack.identifier.Identify(ctx, r); err != nil {
		t.Errorf("issuer b must stay trusted after a failed reload: %v", err)
	}

	// With the ref fixed, the same change goes through, and b is gone.
	t.Setenv("SLUICE_TEST_HMAC_MISSING", "secret-c-32-bytes-padded-!!!!!!!!")
	if _, err := w.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reg.Current().Version != 2 || eng.Snapshot().Digest != "next" {
		t.Errorf("version %d digest %q, want 2 and next", reg.Current().Version, eng.Snapshot().Digest)
	}
	if _, err := stack.identifier.Identify(ctx, r); err == nil {
		t.Error("issuer b must be gone after the reload")
	}
}

// A changed data source still reloads the rest, and says that it needs a
// restart.
func TestReload_DataSourceChangeNeedsRestart(t *testing.T) {
	ds := func(path string) *apitypes.DataSource {
		return &apitypes.DataSource{
			Metadata: apitypes.ObjectMeta{Name: "files"},
			Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: path},
		}
	}
	deps := &runtimeDeps{log: discardLogger(), snapshot: &config.Snapshot{DataSources: []*apitypes.DataSource{ds("/a.db")}}}
	prep, err := deps.prepareReload(context.Background(), &config.Snapshot{DataSources: []*apitypes.DataSource{ds("/a.db")}})
	if err != nil || len(prep.Warnings) != 0 {
		t.Fatalf("same data sources: warnings %v, err %v", prep.Warnings, err)
	}
	prep, err = deps.prepareReload(context.Background(), &config.Snapshot{DataSources: []*apitypes.DataSource{ds("/b.db")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prep.Warnings) != 1 || prep.Warnings[0] != "restart needed: data sources changed" {
		t.Fatalf("warnings = %v, want the restart warning", prep.Warnings)
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
