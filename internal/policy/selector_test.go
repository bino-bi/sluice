// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func compileSelectorT(t *testing.T, sel apitypes.Selector) CompiledSelector {
	t.Helper()
	c, err := compileSelector(sel)
	if err != nil {
		t.Fatalf("compileSelector: %v", err)
	}
	return c
}

func TestSelector_GroupsAndCatalog(t *testing.T) {
	sel := apitypes.Selector{
		Any: []apitypes.Clause{
			{
				Subjects:  &apitypes.SubjectSelector{Groups: []string{"bi-users"}},
				Resources: &apitypes.ResourceSelector{Catalogs: []string{"pg"}},
			},
		},
	}
	cs := compileSelectorT(t, sel)

	user := &identity.UserCtx{Subject: "u", Groups: []string{"bi-users"}}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}}

	if !cs.Match(MatchContext{User: user, Tables: tables}) {
		t.Error("should match bi-users on pg.*")
	}

	user2 := &identity.UserCtx{Subject: "u2", Groups: []string{"external"}}
	if cs.Match(MatchContext{User: user2, Tables: tables}) {
		t.Error("must not match non-bi-users")
	}

	tables2 := []parser.TableRef{{Catalog: "mysql", Schema: "shop", Table: "orders"}}
	if cs.Match(MatchContext{User: user, Tables: tables2}) {
		t.Error("must not match different catalog")
	}
}

func TestSelector_EmptyDenies(t *testing.T) {
	// No clauses → match nothing (default deny is enforced by the engine,
	// not the selector).
	cs := compileSelectorT(t, apitypes.Selector{})
	user := &identity.UserCtx{Subject: "u"}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "t"}}
	if cs.Match(MatchContext{User: user, Tables: tables}) {
		t.Error("empty selector must not match")
	}
}

func TestSelector_WildcardSegments(t *testing.T) {
	sel := apitypes.Selector{
		Any: []apitypes.Clause{
			{Resources: &apitypes.ResourceSelector{Tables: []string{"*_pii"}}},
		},
	}
	cs := compileSelectorT(t, sel)

	for tbl, want := range map[string]bool{
		"customer_pii": true,
		"employee_pii": true,
		"orders":       false,
		"customer":     false,
	} {
		tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: tbl}}
		if got := cs.Match(MatchContext{Tables: tables}); got != want {
			t.Errorf("table %q: got %v, want %v", tbl, got, want)
		}
	}
}

func TestSelector_ClaimOps(t *testing.T) {
	sel := apitypes.Selector{
		Any: []apitypes.Clause{
			{
				Subjects: &apitypes.SubjectSelector{
					JWTClaims: []apitypes.ClaimCheck{
						{Claim: "tenant_id", Op: apitypes.ClaimOpEquals, Value: "t-1"},
					},
				},
				Resources: &apitypes.ResourceSelector{Tables: []string{"*"}},
			},
		},
	}
	cs := compileSelectorT(t, sel)

	u1 := &identity.UserCtx{Subject: "u1", Claims: map[string]any{"tenant_id": "t-1"}}
	u2 := &identity.UserCtx{Subject: "u2", Claims: map[string]any{"tenant_id": "t-9"}}
	tables := []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}}

	if !cs.Match(MatchContext{User: u1, Tables: tables}) {
		t.Error("u1 tenant_id=t-1 must match")
	}
	if cs.Match(MatchContext{User: u2, Tables: tables}) {
		t.Error("u2 tenant_id=t-9 must not match")
	}
}

func TestSelector_Specificity(t *testing.T) {
	broad := compileSelectorT(t, apitypes.Selector{
		Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}}},
	})
	precise := compileSelectorT(t, apitypes.Selector{
		Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{
			Catalogs: []string{"pg"}, Schemas: []string{"hr"}, Tables: []string{"employees"},
		}}},
	})
	if precise.Specificity() <= broad.Specificity() {
		t.Errorf("precise (%d) must beat broad (%d)", precise.Specificity(), broad.Specificity())
	}
}
