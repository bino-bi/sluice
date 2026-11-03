// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/synth/build"
)

const synthSeedEnv = "SLUICE_TEST_SYNTH_SEED"

// synthFixture writes a DuckDB source, a policy directory with the
// DataSources crm and crmsynth, and a server config. It returns the
// paths of the server config, the policy directory and synthetic.dir.
func synthFixture(t *testing.T, maxRatio string) (cfg, policies, out string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "source.duckdb")
	c, err := duckdb.NewConnector(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	_, err = db.Exec(`CREATE SCHEMA crm;
CREATE TABLE crm.customers AS
SELECT i::BIGINT AS id, ['a', 'b', 'c'][i % 3 + 1] AS segment, i * 1.5 AS amount
FROM range(200) t(i);`)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}

	policies = filepath.Join(dir, "policies.d")
	out = filepath.Join(dir, "synthetic")
	cfg = filepath.Join(dir, "sluice.yaml")
	if err := os.MkdirAll(policies, 0o750); err != nil {
		t.Fatal(err)
	}
	manifests := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crm
spec:
  type: duckdb_file
  path: ` + src + `
---
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/` + synthSeedEnv + `
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
`
	if err := os.WriteFile(filepath.Join(policies, "sources.yaml"), []byte(manifests), 0o600); err != nil {
		t.Fatal(err)
	}
	server := "policies:\n  directory: " + policies + "\nsynthetic:\n  dir: " + out + "\n  maxRatio: " + maxRatio + "\n"
	if err := os.WriteFile(cfg, []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(synthSeedEnv, "cli-test-seed-0123456789abcdef")
	return cfg, policies, out
}

func runCLI(args ...string) (stdout, stderr string, code int) {
	root := newRootCmd()
	root.SetArgs(args)
	var o, e bytes.Buffer
	root.SetOut(&o)
	root.SetErr(&e)
	err := root.Execute()
	if err == nil {
		return o.String(), e.String(), 0
	}
	var exit *exitError
	if errors.As(err, &exit) {
		if exit.Err != nil || exit.Msg != "" {
			e.WriteString(exit.Error())
		}
		return o.String(), e.String(), exit.Code
	}
	e.WriteString(err.Error())
	return o.String(), e.String(), 1
}

func TestSynthBuild_SucceedsAndReports(t *testing.T) {
	cfg, _, out := synthFixture(t, "0.2")

	stdout, stderr, code := runCLI("synth", "build", "crmsynth", "--config", cfg)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "succeeded") || !strings.Contains(stdout, "crm.customers") {
		t.Errorf("summary: %q", stdout)
	}

	// The audit record goes to stderr as one JSON line and into the report.
	var rec map[string]any
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, `"event_type":"synthetic-build"`) {
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("audit line: %v", err)
			}
		}
	}
	if rec == nil {
		t.Fatalf("no audit line on stderr: %s", stderr)
	}
	if rec["origin"] != "cli" || rec["decision"] != "allow" {
		t.Errorf("audit record: %v", rec)
	}

	l := build.Layout{Dir: out, Name: "crmsynth"}
	id, err := l.ReadCurrent()
	if err != nil || id == "" {
		t.Fatalf("current: %q, %v", id, err)
	}
	rep, err := l.ReadReport(id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Audit == nil || rep.Audit.Extras["build_id"] != id {
		t.Errorf("report audit: %+v", rep.Audit)
	}
	if got := rep.Tables[0].SyntheticRows; got != 40 {
		t.Errorf("synthetic rows = %d, want 40", got)
	}

	stdout, stderr, code = runCLI("synth", "report", "crmsynth", "--config", cfg, "--json")
	if code != 0 {
		t.Fatalf("report --json: exit %d, %s", code, stderr)
	}
	file, err := os.ReadFile(l.Report(id))
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(file) {
		t.Error("report --json does not print the report file")
	}

	stdout, stderr, code = runCLI("synth", "report", "crmsynth", "--config", cfg, "--build", id)
	if code != 0 {
		t.Fatalf("report: exit %d, %s", code, stderr)
	}
	for _, want := range []string{id, "segment", "amount", "categorical", "distribution"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report text lacks %q: %s", want, stdout)
		}
	}
}

func TestSynthBuild_ExitCodes(t *testing.T) {
	cfg, policies, _ := synthFixture(t, "0.2")
	badCfg := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badCfg, []byte("synthetic:\n  maxRatio: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	brokenPolicies := t.TempDir()
	if err := os.WriteFile(filepath.Join(brokenPolicies, "x.yaml"), []byte("apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: Bad_Name\nspec:\n  type: sqlite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lowCfg, _, lowOut := synthFixture(t, "0.1")

	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"unknown name", []string{"crmx", "--config", cfg}, 1, `no DataSource named "crmx"`},
		{"not synthetic", []string{"crm", "--config", cfg}, 1, "is not synthetic"},
		{"server config not valid", []string{"crmsynth", "--config", badCfg, "--policies-dir", policies}, 3, "synthetic.maxRatio"},
		{"policy not valid", []string{"crmsynth", "--config", cfg, "--policies-dir", brokenPolicies}, 3, ""},
		{"ratio over maxRatio", []string{"crmsynth", "--config", lowCfg}, 4, "synthetic build crmsynth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCLI(append([]string{"synth", "build"}, tc.args...)...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr lacks %q: %s", tc.want, stderr)
			}
		})
	}

	// The failed build keeps its report but does not publish.
	l := build.Layout{Dir: lowOut, Name: "crmsynth"}
	if id, err := l.ReadCurrent(); err != nil || id != "" {
		t.Errorf("current after a failed build: %q, %v", id, err)
	}
}

func TestSynthReport_Errors(t *testing.T) {
	cfg, _, _ := synthFixture(t, "0.2")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"path in build id", []string{"crmsynth", "--config", cfg, "--build", "../x"}, "invalid build id"},
		{"no current build", []string{"crmsynth", "--config", cfg}, "no report"},
		{"unknown build", []string{"crmsynth", "--config", cfg, "--build", "20261102T101500Z-a1b2c3"}, "no report"},
		{"path in name", []string{"../crmsynth", "--config", cfg}, "not a synthetic data source name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runCLI(append([]string{"synth", "report"}, tc.args...)...)
			if code != 1 {
				t.Fatalf("exit %d, want 1; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr lacks %q: %s", tc.want, stderr)
			}
		})
	}
}

func TestPrintBuildWarningsOnce(t *testing.T) {
	r := &build.Report{
		Tables: []build.TableReport{{Name: "main.t", Columns: []build.ColumnReport{
			{Name: "bl", Warnings: []string{"type BLOB, written as NULL"}},
		}}},
		Warnings: []string{"main.t.bl: type BLOB, written as NULL"},
	}
	var buf bytes.Buffer
	printBuildWarnings(&buf, r)
	if got, want := buf.String(), "warning: main.t.bl: type BLOB, written as NULL\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
