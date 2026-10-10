// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func allowTables(name string, tables ...string) *apitypes.SQLAccessPolicy {
	return &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: 100},
		Spec: apitypes.SQLAccessSpec{
			Effect: apitypes.EffectAllow,
			Match: apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{
				Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: tables,
			}}}},
		},
	}
}

var (
	customers  = parser.TableRef{Catalog: "shop", Schema: "main", Table: "customers"}
	adminUsers = parser.TableRef{Catalog: "shop", Schema: "main", Table: "admin_users"}
)

// An allow admits only the tables it names. A table joined to an allowed
// one is not admitted with it.
func TestEngine_AllowDoesNotCoverOtherTables(t *testing.T) {
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowTables("allow-customers", "customers"))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{customers, adminUsers}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Fatalf("outcome = %s, want deny", dec.Outcome)
	}
	if !dec.Abstained {
		t.Errorf("uncovered-table deny must be an abstain, like the no-allow case")
	}
	if dec.DenyReason == nil || !strings.Contains(dec.DenyReason.Message, "shop.main.admin_users") {
		t.Errorf("deny reason = %+v, want it to name the uncovered table", dec.DenyReason)
	}

	// The allowed table on its own is still admitted.
	dec, err = eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{customers}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("customers alone: outcome = %s, want allow", dec.Outcome)
	}
}

// Different tables may be covered by different allow policies.
func TestEngine_TablesCoveredBySeparateAllows(t *testing.T) {
	eng := New(Options{})
	snap := makeSnapshot(allowTables("allow-customers", "customers"), allowTables("allow-admin", "admin_users"))
	if err := eng.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{customers, adminUsers}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("outcome = %s (%+v), want allow", dec.Outcome, dec.DenyReason)
	}
}

// In a composite an OPA member decides on the whole table list, so its
// allow may cover a table the YAML allows do not name. With no member
// allowing it, the statement stays denied. (A ReBAC member never allows on
// its own; internal/rebac tests that.)
func TestComposite_AffirmativeMemberMayCoverYAMLGap(t *testing.T) {
	yaml := New(Options{})
	if err := yaml.ApplySnapshot(context.Background(), makeSnapshot(allowTables("allow-customers", "customers"))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	in := Input{Tables: []parser.TableRef{customers, adminUsers}}

	opa := stubEngine{name: "opa", dec: &Decision{Outcome: OutcomeAllow}}
	dec, err := compositeOf(yaml, opa).Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("with an allowing member: outcome = %s, want allow", dec.Outcome)
	}

	silent := stubEngine{name: "opa", dec: &Decision{Outcome: OutcomeDeny, Abstained: true}}
	dec, err = compositeOf(yaml, silent).Evaluate(context.Background(), in)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("with no allowing member: outcome = %s, want deny", dec.Outcome)
	}
}

// The parser classifies EXPLAIN by its body and keeps StmtExplain only for
// a SELECT body, so an allow scoped to SELECT covers the tables of an
// EXPLAIN SELECT too.
func TestEngine_ExplainCountsAsSelect(t *testing.T) {
	scoped := allowTables("allow-customers-select", "customers")
	scoped.Spec.Match.Any[0].Resources.Actions = []apitypes.Action{apitypes.ActionSelect}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(scoped, allowTables("allow-admin", "admin_users"))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, kind := range []parser.StmtKind{parser.StmtSelect, parser.StmtExplain} {
		dec, err := eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{customers, adminUsers}, AST: stubAST{kind: kind}})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if dec.Outcome != OutcomeAllow {
			t.Errorf("%s: outcome = %s (%+v), want allow", kind, dec.Outcome, dec.DenyReason)
		}
	}
}

func accessPolicy(name string, effect apitypes.Effect, match, exclude *apitypes.ResourceSelector, sub *apitypes.SubjectSelector) *apitypes.SQLAccessPolicy {
	p := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: 100},
		Spec:     apitypes.SQLAccessSpec{Effect: effect, Match: apitypes.Selector{Any: []apitypes.Clause{{Subjects: sub, Resources: match}}}},
	}
	if exclude != nil {
		p.Spec.Exclude = &apitypes.Selector{Any: []apitypes.Clause{{Resources: exclude}}}
	}
	return p
}

// An access policy's Exclude carves out tables, it does not switch the
// policy off for the whole statement. A deny keeps denying the tables it
// still names, and an allow keeps covering them.
func TestEngine_AccessExcludeIsPerTable(t *testing.T) {
	shop := &apitypes.ResourceSelector{Catalogs: []string{"shop"}}
	payroll := parser.TableRef{Catalog: "shop", Schema: "finance", Table: "payroll"}
	rates := parser.TableRef{Catalog: "shop", Schema: "finance", Table: "public_rates"}

	eng := New(Options{})
	deny := accessPolicy("deny-finance", apitypes.EffectDeny,
		&apitypes.ResourceSelector{Schemas: []string{"finance"}}, &apitypes.ResourceSelector{Tables: []string{"public_rates"}}, nil)
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(accessPolicy("allow-shop", apitypes.EffectAllow, shop, nil, nil), deny)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, tc := range []struct {
		tables []parser.TableRef
		want   Outcome
	}{
		{[]parser.TableRef{payroll}, OutcomeDeny},
		{[]parser.TableRef{rates}, OutcomeAllow},
		{[]parser.TableRef{payroll, rates}, OutcomeDeny},
		{[]parser.TableRef{rates, payroll}, OutcomeDeny},
	} {
		dec, err := eng.Evaluate(context.Background(), Input{Tables: tc.tables})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if dec.Outcome != tc.want {
			t.Errorf("deny with carve-out, tables %v: outcome = %s, want %s", tc.tables, dec.Outcome, tc.want)
		}
	}

	eng = New(Options{})
	broad := accessPolicy("allow-shop-except-admin", apitypes.EffectAllow, shop, &apitypes.ResourceSelector{Tables: []string{"admin_users"}}, nil)
	admins := accessPolicy("allow-admin-users", apitypes.EffectAllow,
		&apitypes.ResourceSelector{Tables: []string{"admin_users"}}, nil, &apitypes.SubjectSelector{Groups: []string{"admins"}})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(broad, admins)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	both := []parser.TableRef{customers, adminUsers}
	for _, tc := range []struct {
		groups []string
		want   Outcome
	}{
		{[]string{"admins"}, OutcomeAllow},
		{nil, OutcomeDeny},
	} {
		dec, err := eng.Evaluate(context.Background(), Input{User: &identity.UserCtx{Subject: "u", Groups: tc.groups}, Tables: both})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if dec.Outcome != tc.want {
			t.Errorf("allow with carve-out, groups %v: outcome = %s (%+v), want %s", tc.groups, dec.Outcome, dec.DenyReason, tc.want)
		}
	}

	// A subject carve-out still switches the policy off.
	eng = New(Options{})
	auditorsOut := accessPolicy("allow-shop", apitypes.EffectAllow, shop, nil, nil)
	auditorsOut.Spec.Exclude = &apitypes.Selector{Any: []apitypes.Clause{{Subjects: &apitypes.SubjectSelector{Groups: []string{"auditors"}}}}}
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(auditorsOut)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{User: &identity.UserCtx{Subject: "a", Groups: []string{"auditors"}}, Tables: []parser.TableRef{customers}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny {
		t.Errorf("subject carve-out: outcome = %s, want deny", dec.Outcome)
	}
}

// A deny may name a combination of tables with all-clauses. Applying the
// Exclude per table must keep such a deny working.
func TestEngine_CombinationDenyStillFires(t *testing.T) {
	salaries := parser.TableRef{Catalog: "shop", Schema: "main", Table: "salaries"}
	employees := parser.TableRef{Catalog: "shop", Schema: "main", Table: "employees"}
	combo := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "deny-salary-join", Priority: 200},
		Spec: apitypes.SQLAccessSpec{Effect: apitypes.EffectDeny, Match: apitypes.Selector{All: []apitypes.Clause{
			{Resources: &apitypes.ResourceSelector{Tables: []string{"salaries"}}},
			{Resources: &apitypes.ResourceSelector{Tables: []string{"employees"}}},
		}}},
	}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allowTables("allow-hr", "salaries", "employees"), combo)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{salaries, employees}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeDeny || dec.DenyReason.PolicyName != "deny-salary-join" {
		t.Errorf("combination deny: outcome = %s (%+v), want deny-salary-join", dec.Outcome, dec.DenyReason)
	}
	dec, _ = eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{salaries}})
	if dec.Outcome != OutcomeAllow {
		t.Errorf("one table of the combination: outcome = %s, want allow", dec.Outcome)
	}
}

// An ApprovalPolicy on a table must survive a join to a table that only a
// composite partner allows. The gated engine abstains (uncovered table),
// but it still carries the approval requirement so the partner's allow does
// not strip the hold.
func TestEngine_ApprovalSurvivesUncoveredTableInComposite(t *testing.T) {
	appr := &apitypes.ApprovalPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindApprovalPolicy},
		Metadata: apitypes.ObjectMeta{Name: "approve-customers", Priority: 100},
		Spec: apitypes.ApprovalSpec{
			Match:  apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: []string{"customers"}}}}},
			Reason: "customer data needs sign-off",
		},
	}
	yaml := New(Options{})
	if err := yaml.ApplySnapshot(context.Background(), makeSnapshot(allowTables("allow-customers", "customers"), appr)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	events := parser.TableRef{Catalog: "shop", Schema: "main", Table: "events"}
	opaAllows := stubEngine{name: "opa", dec: &Decision{Outcome: OutcomeAllow}}
	dec, err := compositeOf(yaml, opaAllows).Evaluate(context.Background(), Input{Tables: []parser.TableRef{customers, events}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Fatalf("outcome = %s, want allow (opa covers events)", dec.Outcome)
	}
	if dec.Approval == nil {
		t.Fatal("approval hold on customers was stripped by joining an opa-only table")
	}
}

// An allow whose exclude is an `all` combination (never join A with B)
// keeps the whole-statement drop: a join of the two excluded tables is not
// admitted, while each table on its own still is.
func TestEngine_AllComboExcludeDropsWholePolicy(t *testing.T) {
	allow := accessPolicy("allow-shop-no-join", apitypes.EffectAllow,
		&apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}}, nil, nil)
	allow.Spec.Exclude = &apitypes.Selector{All: []apitypes.Clause{
		{Resources: &apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: []string{"customers"}}},
		{Resources: &apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: []string{"orders"}}},
	}}
	orders := parser.TableRef{Catalog: "shop", Schema: "main", Table: "orders"}
	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), makeSnapshot(allow)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, tc := range []struct {
		tables []parser.TableRef
		want   Outcome
	}{
		{[]parser.TableRef{customers}, OutcomeAllow},
		{[]parser.TableRef{orders}, OutcomeAllow},
		{[]parser.TableRef{customers, orders}, OutcomeDeny},
	} {
		dec, err := eng.Evaluate(context.Background(), Input{Tables: tc.tables})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if dec.Outcome != tc.want {
			t.Errorf("combo exclude, tables %v: outcome = %s, want %s", tc.tables, dec.Outcome, tc.want)
		}
	}
}
