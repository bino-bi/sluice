// SPDX-License-Identifier: AGPL-3.0-or-later

package opaengine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/opaengine"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
)

func writeModule(t *testing.T, rego string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(rego), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newEngine(t *testing.T, rego string) *opaengine.Engine {
	t.Helper()
	e, err := opaengine.New(opaengine.Options{ModuleDir: writeModule(t, rego), Query: "data.sluice.main"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.ApplySnapshot(context.Background(), nil); err != nil {
		t.Fatalf("ApplySnapshot: %v", err)
	}
	return e
}

func inputFor(subject string, groups []string, table parser.TableRef) policy.Input {
	return policy.Input{
		User:   &identity.UserCtx{Subject: subject, Groups: groups},
		Tables: []parser.TableRef{table},
	}
}

var ordersTable = parser.TableRef{Catalog: "pg", Schema: "public", Table: "orders"}

func TestOPA_AllowByGroup(t *testing.T) {
	e := newEngine(t, `
package sluice
import rego.v1
main := {"allow": true} if {
	"analysts" in input.subject.groups
}
main := {"allow": false} if {
	not "analysts" in input.subject.groups
}
`)
	dec, err := e.Evaluate(context.Background(), inputFor("alice", []string{"analysts"}, ordersTable))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != policy.OutcomeAllow {
		t.Errorf("outcome = %s, want allow", dec.Outcome)
	}

	deny, _ := e.Evaluate(context.Background(), inputFor("bob", []string{"guests"}, ordersTable))
	if deny.Outcome != policy.OutcomeDeny {
		t.Errorf("outcome = %s, want deny", deny.Outcome)
	}
}

func TestOPA_RowFilterFromRego(t *testing.T) {
	e := newEngine(t, `
package sluice
import rego.v1
main := {
	"allow": true,
	"row_filters": [{
		"table": "pg.public.orders",
		"combine": "restrictive",
		"predicate": {"column": "tenant_id", "op": "Equals", "value": "acme"},
	}],
}
`)
	dec, err := e.Evaluate(context.Background(), inputFor("alice", nil, ordersTable))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	f, ok := dec.RowFilters["pg.public.orders"]
	if !ok {
		t.Fatalf("row filter missing: %+v", dec.RowFilters)
	}
	if f.Predicate.Column != "tenant_id" {
		t.Errorf("predicate = %+v", f.Predicate)
	}
}

func TestOPA_UnknownTableFailsClosed(t *testing.T) {
	e := newEngine(t, `
package sluice
import rego.v1
main := {
	"allow": true,
	"row_filters": [{"table": "pg.public.secrets", "predicate": {"column": "x", "op": "Equals", "value": "1"}}],
}
`)
	if _, err := e.Evaluate(context.Background(), inputFor("alice", nil, ordersTable)); err == nil {
		t.Fatal("expected error for a table outside the input set")
	}
}

func TestOPA_MalformedOutputFailsClosed(t *testing.T) {
	e := newEngine(t, `
package sluice
import rego.v1
main := {"allow": true, "unexpected_field": 1}
`)
	if _, err := e.Evaluate(context.Background(), inputFor("alice", nil, ordersTable)); err == nil {
		t.Fatal("expected error for unknown output field (strict decode)")
	}
}

func TestOPA_NoModulesFailsClosed(t *testing.T) {
	e, _ := opaengine.New(opaengine.Options{ModuleDir: "/nonexistent"})
	if _, err := e.Evaluate(context.Background(), inputFor("alice", nil, ordersTable)); err == nil {
		t.Fatal("expected fail-closed error with no modules loaded")
	}
}

func TestOPA_Abstain(t *testing.T) {
	e := newEngine(t, `
package sluice
import rego.v1
main := {"abstain": true}
`)
	dec, err := e.Evaluate(context.Background(), inputFor("alice", nil, ordersTable))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != policy.OutcomeDeny || !dec.Abstained {
		t.Errorf("abstain: outcome=%s abstained=%v", dec.Outcome, dec.Abstained)
	}
}
