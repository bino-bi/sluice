// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// makeSnapshot wraps a slice of Objects in a config.Snapshot.
func makeSnapshot(objs ...apitypes.Object) *config.Snapshot {
	s := &config.Snapshot{Policies: objs, ByKind: map[apitypes.Kind][]apitypes.Object{}}
	for _, o := range objs {
		s.ByKind[o.GetKind()] = append(s.ByKind[o.GetKind()], o)
	}
	return s
}

// allowAll is the canonical minimal allow policy used by most fixtures.
func allowAll(priority int32) *apitypes.SQLAccessPolicy {
	return &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "allow-all", Priority: priority},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectAllow,
			Match: apitypes.Selector{
				Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}},
			},
		},
	}
}

func TestEngine_DefaultDeny_NoSnapshot(t *testing.T) {
	eng := New(Options{})
	dec, err := eng.Evaluate(context.Background(), Input{
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("outcome = %s, want deny", dec.Outcome)
	}
}

func TestEngine_DefaultDeny_EmptySnapshot(t *testing.T) {
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("outcome = %s, want deny", dec.Outcome)
	}
	if dec.DenyReason == nil || dec.DenyReason.Code != "ACL_DENIED" {
		t.Errorf("deny reason missing or wrong code: %+v", dec.DenyReason)
	}
}

func TestEngine_AllowSimple(t *testing.T) {
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("outcome = %s, want allow", dec.Outcome)
	}
}

func TestEngine_DenyOverridesAllow(t *testing.T) {
	deny := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "deny-pii", Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			Effect:    apitypes.EffectDeny,
			Message:   "no PII access",
			ErrorCode: "ACL_DENIED",
			Match: apitypes.Selector{
				Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*_pii"}}}},
			},
		},
	}
	snap := makeSnapshot(allowAll(0), deny)
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "customer_pii"}},
	})
	if dec.Outcome != OutcomeDeny {
		t.Fatalf("outcome = %s, want deny", dec.Outcome)
	}
	if dec.DenyReason == nil || dec.DenyReason.PolicyName != "deny-pii" {
		t.Errorf("deny reason = %+v", dec.DenyReason)
	}
}

func TestEngine_RowFilterTemplate(t *testing.T) {
	rf := &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: "tenant-filter"},
		Spec: apitypes.RowFilterSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}}},
			}},
			Filter: apitypes.FilterSpec{
				Predicate: &apitypes.Predicate{
					Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "{{ subject.tenant_id }}",
				},
			},
		},
	}
	snap := makeSnapshot(allowAll(0), rf)
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}},
		Tables: tables,
	})
	if dec.Outcome != OutcomeAllow {
		t.Fatalf("outcome = %s, want allow", dec.Outcome)
	}
	key := "pg.public.orders"
	if _, ok := dec.RowFilters[key]; !ok {
		t.Fatalf("row filter missing; got keys %v", dec.RowFilters)
	}
	filter := dec.RowFilters[key]
	if filter.Predicate == nil || !filter.Predicate.IsLeaf() {
		t.Fatalf("predicate not a leaf: %+v", filter.Predicate)
	}
	if filter.Predicate.Column != "tenant_id" {
		t.Errorf("column = %q", filter.Predicate.Column)
	}
	if len(filter.Predicate.Values) != 1 || filter.Predicate.Values[0].Template == nil {
		t.Fatalf("expected single template value: %+v", filter.Predicate.Values)
	}
}

func TestEngine_ColumnMaskConstant(t *testing.T) {
	mask := &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: "mask-email"},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}, Columns: []string{"email"}}},
			}},
			Mask: apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: "***"}},
		},
	}
	snap := makeSnapshot(allowAll(0), mask)
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "customers"}},
	})
	if dec.Outcome != OutcomeAllow {
		t.Fatalf("outcome = %s, want allow", dec.Outcome)
	}
	key := "pg.public.customers.email"
	m, ok := dec.ColumnMasks[key]
	if !ok {
		t.Fatalf("column mask missing; got %v", dec.ColumnMasks)
	}
	if m.Type != apitypes.MaskConstant || m.Args.Value != "***" {
		t.Errorf("unexpected mask: %+v", m)
	}
}

func TestEngine_QueryRejectStatic(t *testing.T) {
	rej := &apitypes.QueryRejectPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindQueryRejectPolicy},
		Metadata: apitypes.ObjectMeta{Name: "no-cross-cat"},
		Spec: apitypes.QueryRejectSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}},
			}},
			Reject: apitypes.RejectSpec{
				Rules: []apitypes.RejectRule{
					{Name: "no-cross-cat", Message: "no cross catalog", Code: "ACL_REJECTED"},
				},
			},
		},
	}
	snap := makeSnapshot(allowAll(0), rej)
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if dec.Outcome != OutcomeReject {
		t.Fatalf("outcome = %s, want reject", dec.Outcome)
	}
	if len(dec.Rejections) == 0 {
		t.Fatal("expected rejection, got none")
	}
}

func TestCompile_RejectsCELConditions(t *testing.T) {
	p := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "cond-allow"},
		Spec: apitypes.SQLAccessSpec{
			Effect:     apitypes.EffectAllow,
			Match:      apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}}},
			Conditions: []apitypes.Condition{{Name: "business-hours", Expression: "now.hour >= 8"}},
		},
	}
	_, err := Compile(context.Background(), makeSnapshot(p))
	if err == nil {
		t.Fatal("expected ErrConditionUnsupported, got nil")
	}
}

func TestEngine_DeterministicOrdering(t *testing.T) {
	// Two masks on the same column with different priorities — higher
	// priority must win.
	mkMask := func(name string, prio int32, value string) *apitypes.ColumnMaskPolicy {
		return &apitypes.ColumnMaskPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
			Metadata: apitypes.ObjectMeta{Name: name, Priority: prio},
			Spec: apitypes.ColumnMaskSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}, Columns: []string{"email"}}},
				}},
				Mask: apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: value}},
			},
		}
	}
	snap := makeSnapshot(allowAll(0), mkMask("low", 10, "LOW"), mkMask("high", 100, "HIGH"))
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i := 0; i < 5; i++ {
		dec, _ := eng.Evaluate(context.Background(), Input{
			User:   &identity.UserCtx{Subject: "u"},
			Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "customers"}},
		})
		m := dec.ColumnMasks["pg.public.customers.email"]
		if m == nil || m.Policy != "high" {
			t.Fatalf("iter %d: expected 'high' policy to win, got %+v", i, m)
		}
	}
}
