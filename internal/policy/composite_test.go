// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// stubEngine is a fixed-decision PolicyEngine for composite tests.
type stubEngine struct {
	name string
	dec  *Decision
	err  error
}

func (s stubEngine) Evaluate(context.Context, Input) (*Decision, error) { return s.dec, s.err }
func (s stubEngine) Explain(context.Context, Input) (*apitypes.ExplainResult, error) {
	return &apitypes.ExplainResult{}, s.err
}
func (s stubEngine) ApplySnapshot(context.Context, *config.Snapshot) error { return nil }
func (s stubEngine) Name() string                                          { return s.name }

func compositeOf(members ...PolicyEngine) *Composite {
	return NewComposite(Options{}, members...)
}

func TestComposite_DenyOverrides(t *testing.T) {
	deny := stubEngine{name: "a", dec: &Decision{Outcome: OutcomeDeny, DenyReason: &DenyReason{Code: "ACL_DENIED"}}}
	allow := stubEngine{name: "b", dec: &Decision{Outcome: OutcomeAllow}}
	dec, err := compositeOf(allow, deny).Evaluate(context.Background(), Input{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("outcome = %s, want deny (deny-overrides)", dec.Outcome)
	}
}

func TestComposite_ErrorFailsClosed(t *testing.T) {
	boom := stubEngine{name: "a", err: errors.New("backend down")}
	allow := stubEngine{name: "b", dec: &Decision{Outcome: OutcomeAllow}}
	if _, err := compositeOf(allow, boom).Evaluate(context.Background(), Input{}); err == nil {
		t.Fatal("composite did not propagate member error")
	}
}

func TestComposite_AllAbstainedDenies(t *testing.T) {
	a := stubEngine{name: "a", dec: &Decision{Outcome: OutcomeDeny, Abstained: true}}
	b := stubEngine{name: "b", dec: &Decision{Outcome: OutcomeDeny, Abstained: true}}
	dec, _ := compositeOf(a, b).Evaluate(context.Background(), Input{})
	if dec.Outcome != OutcomeDeny || !dec.Abstained {
		t.Errorf("outcome = %s abstained=%v, want abstained deny", dec.Outcome, dec.Abstained)
	}
}

func TestComposite_AbstainedMergesGrants(t *testing.T) {
	// One engine allows; another abstains but carries a row filter. The
	// merged decision must be allow WITH the filter (restriction is safe).
	filter := &CompiledFilter{TableKey: "pg.public.orders", Predicate: &CompiledPredicate{Column: "tenant", Op: apitypes.PredOpEquals, Values: []ValueSource{{Literal: "acme"}}}}
	yamlAbstain := stubEngine{name: "yaml", dec: &Decision{
		Outcome: OutcomeDeny, Abstained: true,
		RowFilters: map[string]*CompiledFilter{"pg.public.orders": filter},
	}}
	opaAllow := stubEngine{name: "opa", dec: &Decision{Outcome: OutcomeAllow}}
	dec, _ := compositeOf(yamlAbstain, opaAllow).Evaluate(context.Background(), Input{})
	if dec.Outcome != OutcomeAllow {
		t.Fatalf("outcome = %s, want allow", dec.Outcome)
	}
	if _, ok := dec.RowFilters["pg.public.orders"]; !ok {
		t.Errorf("abstained engine's row filter was dropped: %+v", dec.RowFilters)
	}
}

func TestComposite_FiltersAndCombine(t *testing.T) {
	f1 := &CompiledFilter{TableKey: "t", Predicate: &CompiledPredicate{Column: "a", Op: apitypes.PredOpEquals, Values: []ValueSource{{Literal: "1"}}}, Policies: []string{"p1"}}
	f2 := &CompiledFilter{TableKey: "t", Predicate: &CompiledPredicate{Column: "b", Op: apitypes.PredOpEquals, Values: []ValueSource{{Literal: "2"}}}, Policies: []string{"p2"}}
	a := stubEngine{dec: &Decision{Outcome: OutcomeAllow, RowFilters: map[string]*CompiledFilter{"t": f1}}}
	b := stubEngine{dec: &Decision{Outcome: OutcomeAllow, RowFilters: map[string]*CompiledFilter{"t": f2}}}
	dec, _ := compositeOf(a, b).Evaluate(context.Background(), Input{})
	merged := dec.RowFilters["t"]
	if merged == nil || len(merged.Predicate.All) != 2 {
		t.Fatalf("filters not AND-combined: %+v", merged)
	}
}

func TestComposite_RejectUnioned(t *testing.T) {
	a := stubEngine{dec: &Decision{Outcome: OutcomeAllow}}
	b := stubEngine{dec: &Decision{Outcome: OutcomeReject, Rejections: []Rejection{{PolicyName: "r", RuleName: "x"}}}}
	dec, _ := compositeOf(a, b).Evaluate(context.Background(), Input{})
	if dec.Outcome != OutcomeReject || len(dec.Rejections) != 1 {
		t.Errorf("reject not unioned: %+v", dec)
	}
}

func TestComposite_Name(t *testing.T) {
	c := compositeOf(stubEngine{name: "yaml"}, stubEngine{name: "opa"})
	if c.Name() != "composite(yaml,opa)" {
		t.Errorf("Name() = %q", c.Name())
	}
}

// Abstained is set on a real engine's default-deny path.
func TestEngine_DefaultDenyIsAbstained(t *testing.T) {
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "t"}},
	})
	if dec.Outcome != OutcomeDeny || !dec.Abstained {
		t.Errorf("default-deny abstained=%v, want true", dec.Abstained)
	}
}

// An EXPLICIT deny is NOT abstained.
func TestEngine_ExplicitDenyNotAbstained(t *testing.T) {
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), denyPIIAll())); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "t"}},
	})
	if dec.Outcome != OutcomeDeny || dec.Abstained {
		t.Errorf("explicit deny abstained=%v, want false", dec.Abstained)
	}
}
