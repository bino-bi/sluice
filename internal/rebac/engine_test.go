// SPDX-License-Identifier: AGPL-3.0-or-later

package rebac_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rebac"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func relPolicy() *apitypes.RelationshipPolicy {
	return &apitypes.RelationshipPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionBeta1, Kind: apitypes.KindRelationshipPolicy},
		Metadata: apitypes.ObjectMeta{Name: "fga-finance"},
		Spec: apitypes.RelationshipSpec{
			Match:   apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Schemas: []string{"finance"}}}}},
			Backend: apitypes.RelationshipBackend{Type: "openfga", Endpoint: "http://fga", StoreID: "s1"},
			Checks:  []apitypes.RelationCheck{{ObjectTemplate: "table:{{schema}}.{{table}}", Relation: "viewer"}},
		},
	}
}

func newEngine(t *testing.T, fake *rebac.Fake) *rebac.Engine {
	t.Helper()
	e := rebac.New(rebac.Options{
		Clock:      func() time.Time { return time.Unix(1_700_000_000, 0) },
		NewChecker: func(apitypes.RelationshipBackend, []byte) rebac.RelationChecker { return fake },
	})
	snap := &config.Snapshot{RelationshipPolicies: []*apitypes.RelationshipPolicy{relPolicy()}}
	if err := e.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	return e
}

var financeTable = parser.TableRef{Catalog: "pg", Schema: "finance", Table: "ledger"}

func input() policy.Input {
	return policy.Input{User: &identity.UserCtx{Subject: "alice"}, Tables: []parser.TableRef{financeTable}}
}

func TestReBAC_AllowWhenRelated(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{"table:finance.ledger#viewer@user:alice": true}}
	dec, err := newEngine(t, fake).Evaluate(context.Background(), input())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != policy.OutcomeAllow {
		t.Errorf("outcome = %s, want allow", dec.Outcome)
	}
}

func TestReBAC_DenyWhenNotRelated(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{}}
	dec, _ := newEngine(t, fake).Evaluate(context.Background(), input())
	if dec.Outcome != policy.OutcomeDeny || dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want explicit deny", dec.Outcome, dec.Abstained)
	}
}

func TestReBAC_BackendErrorFailsClosed(t *testing.T) {
	fake := &rebac.Fake{Err: errors.New("fga down")}
	if _, err := newEngine(t, fake).Evaluate(context.Background(), input()); err == nil {
		t.Fatal("backend error must propagate (fail-closed)")
	}
}

func TestReBAC_AbstainWhenNoPolicyMatches(t *testing.T) {
	fake := &rebac.Fake{}
	e := newEngine(t, fake)
	// A table outside the finance schema matches no policy → abstain.
	dec, _ := e.Evaluate(context.Background(), policy.Input{
		User:   &identity.UserCtx{Subject: "alice"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if dec.Outcome != policy.OutcomeDeny || !dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want abstain", dec.Outcome, dec.Abstained)
	}
}

func TestReBAC_CachesChecks(t *testing.T) {
	fake := &rebac.Fake{Tuples: map[string]bool{"table:finance.ledger#viewer@user:alice": true}}
	e := newEngine(t, fake)
	_, _ = e.Evaluate(context.Background(), input())
	_, _ = e.Evaluate(context.Background(), input())
	if fake.Calls != 1 {
		t.Errorf("checker called %d times, want 1 (cached)", fake.Calls)
	}
}
