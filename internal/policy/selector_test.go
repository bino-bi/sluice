// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"strings"
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

func TestSelector_IPRanges(t *testing.T) {
	cs := compileSelectorT(t, apitypes.Selector{Any: []apitypes.Clause{{
		Subjects: &apitypes.SubjectSelector{IPRanges: []string{"10.0.0.0/8", "2001:db8::/32"}},
	}}})
	for addr, want := range map[string]bool{
		"10.1.2.3":      true,
		"2001:db8::7":   true,
		"192.168.1.1":   false,
		"2001:db9::1":   false,
		"":              false,
		"10.1.2.3:4711": false, // the identifiers store the address without the port
	} {
		user := &identity.UserCtx{Subject: "u", RemoteAddr: addr}
		if got := cs.Match(MatchContext{User: user}); got != want {
			t.Errorf("address %q: match = %v, want %v", addr, got, want)
		}
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

// DuckDB resolves catalog, schema and table names without regard to case,
// so a selector must match them the same way in both directions.
func TestSelector_NamesMatchWithoutRegardToCase(t *testing.T) {
	cs := compileSelectorT(t, apitypes.Selector{Any: []apitypes.Clause{{
		Resources: &apitypes.ResourceSelector{
			Catalogs: []string{"Shop"}, Schemas: []string{"main"}, Tables: []string{"Customers", "ord*"},
		},
	}}})
	for _, ref := range []parser.TableRef{
		{Catalog: "shop", Schema: "main", Table: "customers"},
		{Catalog: "SHOP", Schema: "Main", Table: "CUSTOMERS"},
		{Catalog: "shop", Schema: "main", Table: "ORDERS"},
	} {
		if !cs.Match(MatchContext{Tables: []parser.TableRef{ref}}) {
			t.Errorf("selector must match %+v", ref)
		}
	}
	if cs.Match(MatchContext{Tables: []parser.TableRef{{Catalog: "shop", Schema: "main", Table: "admin_users"}}}) {
		t.Error("selector must not match another table")
	}
}

// A dotted pattern in catalogs, schemas or tables can never match, since
// each field is compared with one name segment. Loading one is an error
// rather than a policy that silently applies to nothing.
func TestCompile_RejectsDottedNamePatterns(t *testing.T) {
	for _, rs := range []apitypes.ResourceSelector{
		{Tables: []string{"shop.main.customers"}},
		{Tables: []string{"**.customers"}},
		{Schemas: []string{"shop.main"}},
		{Catalogs: []string{"a.b"}},
	} {
		if _, err := compileResourceSelector(rs, nil); err == nil || !strings.Contains(err.Error(), "matches one name") {
			t.Errorf("%+v: err = %v, want a dotted-pattern error", rs, err)
		}
	}
	if _, err := compileResourceSelector(apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: []string{"cust*", "**"}}, nil); err != nil {
		t.Errorf("segment patterns must compile: %v", err)
	}
	// A dot that is part of the name is escaped.
	r, err := compileResourceSelector(apitypes.ResourceSelector{Tables: []string{`sales\.2024`}}, nil)
	if err != nil {
		t.Fatalf("escaped dot: %v", err)
	}
	if !r.matches(parser.TableRef{Catalog: "shop", Schema: "main", Table: "sales.2024"}) || r.matches(parser.TableRef{Table: "sales_2024"}) {
		t.Errorf(`tables: ["sales\.2024"] must match exactly the table named sales.2024`)
	}
}
