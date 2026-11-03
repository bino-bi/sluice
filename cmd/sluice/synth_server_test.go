// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parserbackend"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/synth/build"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// synthPolicies allows the group analytics to read crmsynth with a row
// filter on segment, and denies the source crm.
const synthPolicies = `apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-synth
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["crmsynth"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: deny-source
  priority: 200
spec:
  effect: deny
  match:
    any:
      - resources:
          catalogs: ["crm"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: only-a
  priority: 80
spec:
  match:
    any:
      - resources:
          catalogs: ["crmsynth"]
  combine: restrictive
  filter:
    predicate:
      column: segment
      op: Equals
      value: "a"
`

var analyst = &identity.UserCtx{Subject: "alice", Groups: []string{"analytics"}}

// synthServerFixture extends synthFixture with the policies above, an
// audit log and the admin API. onStart sets build.onStart.
func synthServerFixture(t *testing.T, onStart string) (cfg, policies, out string) {
	t.Helper()
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	cfg, policies, out = synthFixture(t, "0.2")
	if onStart != "" {
		editSources(t, policies, "    tables:", "    build:\n      onStart: "+onStart+"\n    tables:")
	}
	if err := os.WriteFile(filepath.Join(policies, "access.yaml"), []byte(synthPolicies), 0o600); err != nil {
		t.Fatal(err)
	}
	server := "policies:\n  directory: " + policies +
		"\nsynthetic:\n  dir: " + out + "\n  maxRatio: 0.2" +
		"\naudit:\n  file:\n    path: " + filepath.Join(filepath.Dir(cfg), "audit") +
		"\nadmin:\n  enabled: true\n  listen: 127.0.0.1:0\n"
	if err := os.WriteFile(cfg, []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, policies, out
}

func editSources(t *testing.T, policies, old, repl string) {
	t.Helper()
	p := filepath.Join(policies, "sources.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("sources.yaml lacks %q", old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, repl, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func startRuntime(t *testing.T, cfg, policies string) (deps *runtimeDeps, stop func()) {
	t.Helper()
	deps, err := buildRuntime(context.Background(), cfg, policies)
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	closed := false
	stop = func() {
		if closed {
			return
		}
		closed = true
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		deps.Close(ctx)
	}
	t.Cleanup(stop)
	return deps, stop
}

func adminCall(t *testing.T, deps *runtimeDeps, method, path string) (int, []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	deps.admin.Handler().ServeHTTP(w, httptest.NewRequest(method, path, nil))
	body, _ := io.ReadAll(w.Body)
	return w.Code, body
}

func synthStatus(t *testing.T, deps *runtimeDeps) build.StatusView {
	t.Helper()
	code, body := adminCall(t, deps, http.MethodGet, "/admin/synthetic/crmsynth")
	if code != http.StatusOK {
		t.Fatalf("GET status: %d %s", code, body)
	}
	var v build.StatusView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// waitSynthBuild waits until the build id (or, when empty, the newest
// build) has finished and returns it.
func waitSynthBuild(t *testing.T, deps *runtimeDeps, id string) build.BuildView {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, b := range synthStatus(t, deps).Builds {
			if (id == "" || b.BuildID == id) && b.State != build.StateRunning {
				return b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("build %q did not finish", id)
	return build.BuildView{}
}

func adminBuild(t *testing.T, deps *runtimeDeps) build.BuildView {
	t.Helper()
	code, body := adminCall(t, deps, http.MethodPost, "/admin/synthetic/crmsynth/build")
	if code != http.StatusAccepted {
		t.Fatalf("POST build: %d %s", code, body)
	}
	var resp struct {
		BuildID string `json:"build_id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	b := waitSynthBuild(t, deps, resp.BuildID)
	if b.State != build.ResultSucceeded {
		t.Fatalf("admin build: %+v", b)
	}
	return b
}

func dataSourceStatus(t *testing.T, deps *runtimeDeps, name string) map[string]any {
	t.Helper()
	code, body := adminCall(t, deps, http.MethodGet, "/admin/datasources")
	if code != http.StatusOK {
		t.Fatalf("GET datasources: %d", code)
	}
	var resp struct {
		DataSources []map[string]any `json:"datasources"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.DataSources {
		if d["name"] == name {
			return d
		}
	}
	t.Fatalf("data source %s not listed: %s", name, body)
	return nil
}

func sluiceQuery(t *testing.T, deps *runtimeDeps, sql string) (*queryservice.QueryResult, error) {
	t.Helper()
	return deps.service.Execute(context.Background(), queryservice.QueryRequest{
		SQL:    sql,
		User:   analyst,
		Origin: queryservice.OriginREST,
	})
}

// readAll reads the remaining rows of a result as strings.
func readAll(t *testing.T, rows executor.RowIterator) []string {
	t.Helper()
	var out []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Trim(stringOf(v), `"`))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func stringOf(v any) string {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case string:
		return x
	case json.RawMessage:
		return string(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func segments(t *testing.T, deps *runtimeDeps) []string {
	t.Helper()
	res, err := sluiceQuery(t, deps, "SELECT segment FROM crmsynth.crm.customers")
	if err != nil {
		t.Fatalf("query crmsynth: %v", err)
	}
	defer func() { _ = res.Rows.Close() }()
	return readAll(t, res.Rows)
}

func directCount(t *testing.T, deps *runtimeDeps, sql string) string {
	t.Helper()
	resp, err := deps.exec.Execute(context.Background(), executor.Request{SQL: sql})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer func() { _ = resp.Rows.Close() }()
	got := readAll(t, resp.Rows)
	if len(got) != 1 {
		t.Fatalf("%s: %v", sql, got)
	}
	return got[0]
}

func assertOnlyA(t *testing.T, deps *runtimeDeps) {
	t.Helper()
	segs := segments(t, deps)
	want := directCount(t, deps, "SELECT count(*) FROM crmsynth.crm.customers WHERE segment = 'a'")
	if len(segs) == 0 || want != itoa(len(segs)) {
		t.Fatalf("filtered rows: %d, direct count of a: %s", len(segs), want)
	}
	for _, s := range segs {
		if s != "a" {
			t.Fatalf("row filter not applied: segment %q", s)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Test 10: a CLI build is served at the next start, under the policies
// of the synthetic catalog.
func TestSynthServer_QueryThroughSluice(t *testing.T) {
	cfg, policies, _ := synthServerFixture(t, "")
	if _, stderr, code := runCLI("synth", "build", "crmsynth", "--config", cfg); code != 0 {
		t.Fatalf("synth build: exit %d, %s", code, stderr)
	}
	deps, _ := startRuntime(t, cfg, policies)

	if st := synthStatus(t, deps); st.Status != "ready" || st.Current == nil || st.Report == nil {
		t.Fatalf("status: %+v", st)
	}
	if got := directCount(t, deps, "SELECT count(*) FROM crmsynth.crm.customers"); got != "40" {
		t.Errorf("rows in the snapshot: %s, want 40", got)
	}
	assertOnlyA(t, deps)

	_, err := sluiceQuery(t, deps, "SELECT segment FROM crm.crm.customers")
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeACLDenied {
		t.Fatalf("query on the source: %v, want ACL_DENIED", err)
	}
}

// Test 11: without a snapshot the catalog is empty and healthy. An admin
// build swaps the snapshot in, also while a query streams.
func TestSynthServer_NotBuiltThenSwap(t *testing.T) {
	cfg, policies, _ := synthServerFixture(t, "")
	deps, _ := startRuntime(t, cfg, policies)

	st := synthStatus(t, deps)
	if st.Status != "not_built" || st.Current != nil || st.Report != nil || len(st.Builds) != 0 {
		t.Fatalf("status before a build: %+v", st)
	}
	if ds := dataSourceStatus(t, deps, "crmsynth"); ds["status"] != "not_built" {
		t.Errorf("datasources entry: %v", ds)
	}
	deadline := time.Now().Add(10 * time.Second)
	for dataSourceStatus(t, deps, "crmsynth")["healthy"] != true {
		if time.Now().After(deadline) {
			t.Fatal("crmsynth is not healthy before a build")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := directCount(t, deps, "SELECT count(*) FROM information_schema.tables WHERE table_catalog = 'crmsynth'"); got != "0" {
		t.Errorf("tables before a build: %s", got)
	}

	first := adminBuild(t, deps)
	st = synthStatus(t, deps)
	if st.Status != "ready" || st.Current == nil || st.Current.BuildID != first.BuildID || first.Origin != "admin" {
		t.Fatalf("status after the build: %+v", st)
	}
	if ds := dataSourceStatus(t, deps, "crmsynth"); ds["status"] != "ready" || ds["healthy"] != true {
		t.Errorf("datasources entry after the build: %v", ds)
	}
	assertOnlyA(t, deps)
	tables, _, err := deps.service.ListTables(context.Background(), "crmsynth", "", analyst, queryservice.Page{Limit: 10})
	if err != nil || len(tables) != 1 || tables[0].Table != "customers" {
		t.Fatalf("tables after the build: %v, %v", tables, err)
	}

	// A result that is open during the swap reads to the end.
	res, err := sluiceQuery(t, deps, "SELECT segment FROM crmsynth.crm.customers")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rows.Next() {
		t.Fatalf("no first row: %v", res.Rows.Err())
	}
	second := adminBuild(t, deps)
	rest := readAll(t, res.Rows)
	_ = res.Rows.Close()
	if len(rest) == 0 {
		t.Error("the open result lost its rows")
	}
	if st := synthStatus(t, deps); st.Current == nil || st.Current.BuildID != second.BuildID || len(st.Builds) != 2 {
		t.Fatalf("status after the second build: %+v", st)
	}
	assertOnlyA(t, deps)
}

// A CLI build moves current on disk, but the status names the build the
// server serves, also after retention removed its report.
func TestSynthServer_StatusShowsServedBuild(t *testing.T) {
	cfg, policies, _ := synthServerFixture(t, "")
	deps, _ := startRuntime(t, cfg, policies)
	served := adminBuild(t, deps)

	for range 2 {
		// Build ids sort by the second they start in.
		time.Sleep(1100 * time.Millisecond)
		if _, stderr, code := runCLI("synth", "build", "crmsynth", "--config", cfg); code != 0 {
			t.Fatalf("synth build: exit %d, %s", code, stderr)
		}
		st := synthStatus(t, deps)
		if st.Current == nil || st.Current.BuildID != served.BuildID {
			t.Fatalf("status after a CLI build: %+v, want current %s", st.Current, served.BuildID)
		}
		if st.Report != nil && st.Report.BuildID != served.BuildID {
			t.Errorf("report of build %s, want %s", st.Report.BuildID, served.BuildID)
		}
	}
	assertOnlyA(t, deps)
}

// build.onStart ifMissing builds a missing snapshot after the start.
func TestSynthServer_OnStartIfMissing(t *testing.T) {
	cfg, policies, _ := synthServerFixture(t, "ifMissing")
	deps, _ := startRuntime(t, cfg, policies)
	deps.synth.StartMissing()
	b := waitSynthBuild(t, deps, "")
	if b.State != build.ResultSucceeded || b.Origin != "startup" {
		t.Fatalf("start-time build: %+v", b)
	}
	if st := synthStatus(t, deps); st.Status != "ready" {
		t.Fatalf("status: %+v", st)
	}
	// Built now: a second call starts nothing.
	deps.synth.StartMissing()
	if n := len(synthStatus(t, deps).Builds); n != 1 {
		t.Errorf("builds after a second StartMissing: %d", n)
	}
}

// Test 12: a changed spec shows stale at the next start, and the old
// snapshot still serves.
func TestSynthServer_Stale(t *testing.T) {
	cfg, policies, _ := synthServerFixture(t, "")
	deps, stop := startRuntime(t, cfg, policies)
	adminBuild(t, deps)
	stop()

	editSources(t, policies, "        size: { ratio: 0.2 }", "        size: { ratio: 0.2 }\n        columns:\n          amount: { generator: uniform }")
	deps, _ = startRuntime(t, cfg, policies)
	if st := synthStatus(t, deps); st.Status != "stale" || st.Current == nil || st.Current.SpecHash == st.SpecHash {
		t.Fatalf("status: %+v", st)
	}
	code, body := adminCall(t, deps, http.MethodGet, "/metrics")
	if code != http.StatusOK || !strings.Contains(string(body), `sluice_synthetic_stale{name="crmsynth"} 1`) {
		t.Errorf("metrics: %d, stale gauge missing", code)
	}
	assertOnlyA(t, deps)
}

// Test 25: the query pool may read the snapshot folder, not the reports
// next to it.
func TestSynthServer_ReportsOutsideAllowedPaths(t *testing.T) {
	cfg, policies, out := synthServerFixture(t, "")
	if _, stderr, code := runCLI("synth", "build", "crmsynth", "--config", cfg); code != 0 {
		t.Fatalf("synth build: exit %d, %s", code, stderr)
	}
	l := build.Layout{Dir: out, Name: "crmsynth"}
	id, err := l.ReadCurrent()
	if err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(l.SnapshotsDir(), "control.txt")
	if err := os.WriteFile(control, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps, _ := startRuntime(t, cfg, policies)

	read := func(path string) error {
		resp, err := deps.exec.Execute(context.Background(), executor.Request{
			SQL: "SELECT content FROM read_text('" + path + "')",
		})
		if err != nil {
			return err
		}
		defer func() { _ = resp.Rows.Close() }()
		for resp.Rows.Next() {
		}
		return resp.Rows.Err()
	}
	if err := read(control); err != nil {
		t.Fatalf("read of a file in the snapshot folder: %v", err)
	}
	report := filepath.Join(l.SnapshotsDir(), "..", "reports", id+".json")
	if err := read(report); err == nil {
		t.Fatal("the query pool read a report")
	}
	if err := read(l.Report(id)); err == nil {
		t.Fatal("the query pool read a report by its clean path")
	}
}
