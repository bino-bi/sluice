// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/secrets"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// The stdio identity of `sluice mcp` is pinned at start. A reload that
// revokes its API key, or moves it to other groups, stops the command
// instead of serving with the rights of the old config.
func TestRecheckPinned(t *testing.T) {
	pepper := "test-pepper"
	t.Setenv("SLUICE_TEST_PEPPER", pepper)
	t.Setenv("SLUICE_TEST_KEY_HASH", hex.EncodeToString(identity.ComputeHash([]byte(pepper), "k1", "material")))
	const apiKey = "k1.material"
	binding := func(groups ...string) *config.Snapshot {
		return &config.Snapshot{SubjectBindings: []*apitypes.SubjectBinding{{
			Metadata: apitypes.ObjectMeta{Name: "agents"},
			Spec: apitypes.SubjectBindingSpec{
				Claims:  apitypes.ClaimPaths{SubjectID: "agent"},
				APIKeys: []apitypes.APIKeyBinding{{ID: "k1", HashRef: "secret://env/SLUICE_TEST_KEY_HASH", Groups: groups}},
			},
		}}}
	}

	for _, tc := range []struct {
		name     string
		next     *config.Snapshot
		wantStop bool
	}{
		{"unchanged", binding("analytics"), false},
		{"key removed", &config.Snapshot{}, true},
		{"groups changed", binding("admins"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scfg := config.DefaultServerConfig()
			scfg.Identity.APIKeyPepper = "secret://env/SLUICE_TEST_PEPPER"
			resolver := secrets.NewResolver(secrets.ResolverOptions{Logger: discardLogger()})
			boot := binding("analytics")
			stack, err := buildIdentity(ctx, &scfg, resolver, boot, discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			deps := &runtimeDeps{
				log: discardLogger(), resolver: resolver, registry: config.NewRegistry(),
				identifier: stack.identifier, apikey: stack.apikey, jwtID: stack.jwt, jwtBindings: stack.bindings,
			}
			deps.registry.Publish(boot)
			pinned, err := resolveStaticIdentity(ctx, deps.identifier, "", apiKey)
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			recheckPinned(deps, &pinnedCred{user: pinned, apiKey: apiKey}, cancel)

			prep, err := deps.prepareReload(ctx, tc.next)
			if err != nil {
				t.Fatal(err)
			}
			deps.registry.PublishWith(tc.next, prep.Apply)

			stopped := errors.Is(context.Cause(runCtx), errPinnedIdentity)
			if stopped != tc.wantStop {
				t.Fatalf("stopped = %v (cause %v), want %v", stopped, context.Cause(runCtx), tc.wantStop)
			}
		})
	}
}

// `sluice mcp` refuses to start with an ApprovalPolicy. A reload must not
// add one either: a held query could never be released.
func TestReload_RefusesApprovalPolicyInMCP(t *testing.T) {
	snap := &config.Snapshot{ByKind: map[apitypes.Kind][]apitypes.Object{
		apitypes.KindApprovalPolicy: {&apitypes.ApprovalPolicy{}},
	}}
	deps := &runtimeDeps{log: discardLogger(), refuseApprovals: true}
	if _, err := deps.prepareReload(context.Background(), snap); err == nil {
		t.Fatal("a reload that adds an ApprovalPolicy must fail in sluice mcp")
	}
}
