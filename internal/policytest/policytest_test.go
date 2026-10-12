// SPDX-License-Identifier: AGPL-3.0-or-later

package policytest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/policytest"
)

// requireRealParser skips a test that cannot run against the pure_parser
// stub backend: NewRunner refuses it at construction time by design.
func requireRealParser(t *testing.T) {
	t.Helper()
	if !parserbackend.Implemented {
		t.Skip("NewRunner refuses the stub parser backend (-tags=pure_parser)")
	}
}

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

// newRunner builds a runner and closes its DuckDB instance after the test.
func newRunner(t *testing.T, dir string) *policytest.Runner {
	t.Helper()
	runner, err := policytest.NewRunner(context.Background(), dir, false)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	t.Cleanup(func() {
		if err := runner.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return runner
}

func TestRunner_PassAndFail(t *testing.T) {
	requireRealParser(t)
	dir := writePolicies(t)
	runner := newRunner(t, dir)

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

	rep, err := runner.Run(context.Background(), suite)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Total != 3 || rep.Passed != 2 || rep.Failed != 1 {
		t.Fatalf("report = %+v, want 3 total / 2 passed / 1 failed", rep)
	}
	if rep.Cases[2].Passed {
		t.Error("third case should have failed on the outcome mismatch")
	}
}

const maskPoliciesYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: { name: allow-all, priority: 0 }
spec:
  effect: allow
  match:
    any:
      - resources: { tables: ["*"] }
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: mask-email, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["customers"], columns: ["email"] }
  mask:
    type: constant
    args:
      value: "redacted@example.com"
`

func TestRunner_SchemaFixtureExpandsStar(t *testing.T) {
	requireRealParser(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policies.yaml"), []byte(maskPoliciesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(t, dir)

	starCase := policytest.Case{
		Name:     "star expands against fixture schema",
		Identity: policytest.Identity{Subject: "alice"},
		SQL:      "SELECT * FROM pg.public.customers",
		Expect: policytest.Expect{
			Outcome:              "allow",
			RewrittenSQLContains: []string{"'redacted@example.com' AS email"},
		},
	}

	suite := &policytest.Suite{
		Schema: map[string][]string{"pg.public.customers": {"id", "email"}},
		Cases:  []policytest.Case{starCase},
	}
	rep, err := runner.Run(context.Background(), suite)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 0 {
		t.Fatalf("with schema fixture: %+v", rep.Cases)
	}

	// The same case without a schema block still fails with the
	// schema-missing rewrite error.
	rep, err = runner.Run(context.Background(), &policytest.Suite{Cases: []policytest.Case{starCase}})
	if err != nil {
		t.Fatalf("Run without schema: %v", err)
	}
	if rep.Failed != 1 {
		t.Fatalf("without schema fixture the star case must fail, got %+v", rep.Cases)
	}

	_, err = runner.Run(context.Background(), &policytest.Suite{
		Schema: map[string][]string{"customers": {"id"}},
		Cases:  []policytest.Case{starCase},
	})
	if err == nil || !strings.Contains(err.Error(), "customers") {
		t.Fatalf("malformed schema key must error naming the key, got %v", err)
	}
}

func TestRunner_RunFile(t *testing.T) {
	requireRealParser(t)
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
	runner := newRunner(t, dir)
	rep, err := runner.RunFile(context.Background(), suitePath)
	if err != nil {
		t.Fatalf("RunFile: %v", err)
	}
	if rep.Failed != 0 || rep.Passed != 1 {
		t.Fatalf("report = %+v, want 1 passed", rep)
	}
}

// Both rewriters run the DuckDB parse check, as the server does. The
// deparse of (a OR b) IS NULL drops the parentheses.
func TestRunner_ParseCheck(t *testing.T) {
	requireRealParser(t)
	runner := newRunner(t, writePolicies(t))

	refused := policytest.Case{
		Name:     "dropped parentheses",
		Identity: policytest.Identity{Subject: "alice", Groups: []string{"analysts"}, Claims: map[string]any{"tenant_id": "acme"}},
		SQL:      "SELECT id FROM pg.public.orders WHERE (id = 1 OR total > 2) IS NULL",
		Expect:   policytest.Expect{Outcome: "allow", ErrorCode: "ERR_UNSUPPORTED_SYNTAX"},
	}
	for _, s := range []*policytest.Suite{
		{Cases: []policytest.Case{refused}},
		{Cases: []policytest.Case{refused}, Schema: map[string][]string{"pg.public.orders": {"id", "total", "tenant_id"}}},
	} {
		rep, err := runner.Run(context.Background(), s)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Failed != 0 {
			t.Errorf("schema %v: %+v", s.Schema, rep.Cases)
		}
	}
}

// A case that sets only errorCode runs the rewrite and fails when the
// rewrite succeeds.
func TestRunner_ErrorCodeChecked(t *testing.T) {
	requireRealParser(t)
	runner := newRunner(t, writePolicies(t))
	rep, err := runner.Run(context.Background(), &policytest.Suite{Cases: []policytest.Case{{
		Name:     "rewrites fine",
		Identity: policytest.Identity{Subject: "alice", Groups: []string{"analysts"}, Claims: map[string]any{"tenant_id": "acme"}},
		SQL:      "SELECT id FROM pg.public.orders",
		Expect:   policytest.Expect{Outcome: "allow", ErrorCode: "ERR_UNSUPPORTED_SYNTAX"},
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 {
		t.Errorf("report = %+v, want the case to fail", rep.Cases)
	}
}

// The rewriters know DuckDB's default database, as on the server, so they
// refuse memory.orders.id on a filtered orders. The filter wrapper is named
// orders alone.
func TestRunner_DefaultDatabaseQualifier(t *testing.T) {
	requireRealParser(t)
	runner := newRunner(t, writePolicies(t))
	refused := policytest.Case{
		Name:     "default database qualifier",
		Identity: policytest.Identity{Subject: "alice", Groups: []string{"analysts"}, Claims: map[string]any{"tenant_id": "acme"}},
		SQL:      "SELECT memory.orders.id FROM orders",
		Expect:   policytest.Expect{Outcome: "allow", ErrorCode: "ERR_UNSUPPORTED_SYNTAX"},
	}
	for _, s := range []*policytest.Suite{
		{Cases: []policytest.Case{refused}},
		{Cases: []policytest.Case{refused}, Schema: map[string][]string{"memory.main.orders": {"id", "tenant_id"}}},
	} {
		rep, err := runner.Run(context.Background(), s)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Failed != 0 {
			t.Errorf("schema %v: %+v", s.Schema, rep.Cases)
		}
	}
}

// An allow case fails when its rewrite fails, also without a rewrite
// assertion. A masked table without a schema entry cannot be rewritten.
func TestRunner_AllowCaseFailsOnRewriteError(t *testing.T) {
	requireRealParser(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policies.yaml"), []byte(maskPoliciesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(t, dir)
	rep, err := runner.Run(context.Background(), &policytest.Suite{Cases: []policytest.Case{{
		Name:     "masked table without schema",
		Identity: policytest.Identity{Subject: "alice"},
		SQL:      "SELECT id, email FROM pg.public.customers",
		Expect:   policytest.Expect{Outcome: "allow", Masks: []string{"pg.public.customers.email=constant"}},
	}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 || len(rep.Cases[0].Failures) != 1 {
		t.Fatalf("report = %+v, want the case to fail once", rep.Cases)
	}
	if msg := rep.Cases[0].Failures[0]; !strings.Contains(msg, "pg.public.customers") || !strings.Contains(msg, "schema:") {
		t.Errorf("failure = %q, want the table and a hint to add schema:", msg)
	}
}

const postMaskPolicyYAML = `
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata: { name: fake-name, priority: 50 }
spec:
  match:
    any:
      - resources: { tables: ["employees"], columns: ["name"] }
  mask:
    type: fake
    args:
      fakeType: name
`

// errorCode asserts the code the server returns for a refused rewrite,
// also when the rewriter returns a plain sentinel error.
func TestRunner_ErrorCodeOfRewriteSentinels(t *testing.T) {
	requireRealParser(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policies.yaml"), []byte(maskPoliciesYAML+postMaskPolicyYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(t, dir)
	for _, s := range []*policytest.Suite{
		{Cases: []policytest.Case{{
			Name:     "masked table without schema",
			Identity: policytest.Identity{Subject: "alice"},
			SQL:      "SELECT id FROM pg.public.customers",
			Expect:   policytest.Expect{Outcome: "allow", ErrorCode: "ERR_REWRITE_FAILED"},
		}}},
		{
			Schema: map[string][]string{"pg.hr.employees": {"id", "name"}},
			Cases: []policytest.Case{{
				Name:     "post-query masked column in WHERE",
				Identity: policytest.Identity{Subject: "alice"},
				SQL:      "SELECT id FROM pg.hr.employees WHERE name = 'x'",
				Expect:   policytest.Expect{Outcome: "allow", ErrorCode: "ERR_MASK_UNSUPPORTED_CONTEXT"},
			}},
		},
	} {
		rep, err := runner.Run(context.Background(), s)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Failed != 0 {
			t.Errorf("%s: %v", rep.Cases[0].Name, rep.Cases[0].Failures)
		}
	}
}
