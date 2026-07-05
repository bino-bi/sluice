// SPDX-License-Identifier: AGPL-3.0-or-later

package policytest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/policytest"
)

const policiesYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-analysts, priority: 0 }
spec:
  effect: allow
  match:
    any:
      - subjects: { groups: [analysts] }
        resources: { tables: ["*"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata: { name: tenant, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["orders"] }
  filter:
    predicate: { column: tenant_id, op: Equals, value: "{{ subject.tenant_id }}" }
`

func writePolicies(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policies.yaml"), []byte(policiesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunner_PassAndFail(t *testing.T) {
	dir := writePolicies(t)
	runner, err := policytest.NewRunner(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	suite := &policytest.Suite{Cases: []policytest.Case{
		{
			Name:     "allow with filter",
			Identity: policytest.Identity{Subject: "alice", Groups: []string{"analysts"}, Claims: map[string]any{"tenant_id": "acme"}},
			SQL:      "SELECT id FROM pg.public.orders",
			Expect: policytest.Expect{
				Outcome:              "allow",
				Filters:              []string{"pg.public.orders"},
				RewrittenSQLContains: []string{"tenant_id = $1"},
			},
		},
		{
			Name:     "default deny",
			Identity: policytest.Identity{Subject: "bob", Groups: []string{"guests"}},
			SQL:      "SELECT id FROM pg.public.orders",
			Expect:   policytest.Expect{Outcome: "deny"},
		},
		{
			Name:     "wrong expectation fails",
			Identity: policytest.Identity{Subject: "alice", Groups: []string{"analysts"}, Claims: map[string]any{"tenant_id": "acme"}},
			SQL:      "SELECT id FROM pg.public.orders",
			Expect:   policytest.Expect{Outcome: "deny"}, // actually allow
		},
	}}

	rep := runner.Run(context.Background(), suite)
	if rep.Total != 3 || rep.Passed != 2 || rep.Failed != 1 {
		t.Fatalf("report = %+v, want 3 total / 2 passed / 1 failed", rep)
	}
	if rep.Cases[2].Passed {
		t.Error("third case should have failed on the outcome mismatch")
	}
}

func TestRunner_RunFile(t *testing.T) {
	dir := writePolicies(t)
	suitePath := filepath.Join(t.TempDir(), "suite.yaml")
	suite := `
cases:
  - name: analyst allowed
    identity: { subject: alice, groups: [analysts], claims: { tenant_id: acme } }
    sql: "SELECT id FROM pg.public.orders"
    expect:
      outcome: allow
      applied: ["RowFilterPolicy/tenant", "SqlAccessPolicy/allow-analysts"]
`
	if err := os.WriteFile(suitePath, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	runner, err := policytest.NewRunner(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	rep, err := runner.RunFile(context.Background(), suitePath)
	if err != nil {
		t.Fatalf("RunFile: %v", err)
	}
	if rep.Failed != 0 || rep.Passed != 1 {
		t.Fatalf("report = %+v, want 1 passed", rep)
	}
}
