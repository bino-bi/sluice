// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/parserbackend"
)

const testChainKey = "0123456789abcdef0123456789abcdef"

// writeAuditDir writes a genesis record and one query record, with key
// when it is set.
func writeAuditDir(t *testing.T, key string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "audit")
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{fs}, GenesisSeed: []byte("cli"), ChainKey: []byte(key),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeGapDir writes a record, leaves out the next one as a failed write
// would, and writes the audit-gap record for it.
func writeGapDir(t *testing.T) string {
	t.Helper()
	seal := func(prior string, r *audit.Record) *audit.Record {
		r.PriorHash = prior
		h, err := audit.ComputeHash(prior, r)
		if err != nil {
			t.Fatal(err)
		}
		r.Hash = h
		return r
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first := seal("anchor", &audit.Record{Timestamp: ts, EventType: audit.EventGenesis})
	lost := seal(first.Hash, &audit.Record{Timestamp: ts, EventType: audit.EventQuery})
	gap := seal(lost.Hash, &audit.Record{Timestamp: ts, EventType: audit.EventAuditGap, Extras: map[string]any{
		audit.GapLostRecords: 1, audit.GapLastWritten: first.Hash,
	}})
	var data []byte
	for _, r := range []*audit.Record{first, gap} {
		line, err := audit.MarshalLine(r)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, line...)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audit-2026-01-02.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// verifyExit runs `sluice audit verify` and returns its exit status and
// output.
func verifyExit(t *testing.T, args ...string) (int, string) {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(append([]string{"audit", "verify"}, args...))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	var exit *exitError
	switch {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &exit):
		return exit.Code, out.String()
	default:
		return 1, out.String()
	}
}

func TestAuditVerify_ExitStatus(t *testing.T) {
	t.Setenv("SLUICE_TEST_CHAIN_KEY", testChainKey+"\n")
	t.Setenv("SLUICE_TEST_OTHER_KEY", strings.Repeat("z", 32))
	plain, keyed := writeAuditDir(t, ""), writeAuditDir(t, testChainKey)
	const ref, other = "secret://env/SLUICE_TEST_CHAIN_KEY", "secret://env/SLUICE_TEST_OTHER_KEY"

	for _, tc := range []struct {
		name string
		args []string
		want int
		text string
	}{
		{"plain chain", []string{plain}, 0, "not keyed"},
		{"plain chain with a key", []string{plain, "--key-ref", ref}, 4, "no keyed record"},
		{"keyed chain with its key", []string{keyed, "--key-ref", ref}, 0, "2 keyed"},
		{"keyed chain without a key", []string{keyed}, 1, ""},
		{"keyed chain with another key", []string{keyed, "--key-ref", other}, 4, "hash mismatch"},
		{"directory without a record", []string{t.TempDir()}, 4, "no audit record"},
		{"chain with a gap", []string{writeGapDir(t)}, 5, "audit-2026-01-02.jsonl:2: 1 record(s) lost"},
		{"chain with a gap as JSON", []string{writeGapDir(t), "--json"}, 5, `"lost_records":1`},
		{"key reference that does not resolve", []string{keyed, "--key-ref", "secret://env/SLUICE_TEST_NO_SUCH"}, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := verifyExit(t, tc.args...)
			if code != tc.want || !strings.Contains(out, tc.text) {
				t.Errorf("exit %d, output %q, want exit %d with %q", code, out, tc.want, tc.text)
			}
		})
	}
}

// audit.chainKeyRef reaches the dispatcher: the records of the runtime are
// keyed, and a key that is too short stops the start.
func TestBuildRuntime_ChainKeyIsWired(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	t.Setenv("SLUICE_TEST_CHAIN_KEY", testChainKey)
	t.Setenv("SLUICE_TEST_SHORT_KEY", "short")
	t.Setenv("SLUICE_TEST_EMPTY_KEY", "\n")
	if _, err := buildMCPRuntime(t, "  chainKeyRef: secret://env/SLUICE_TEST_EMPTY_KEY\n"); err == nil || !strings.Contains(err.Error(), "audit.chainKeyRef") {
		t.Errorf("start with an empty key: %v, want a refusal", err)
	}
	deps, err := buildMCPRuntime(t, "  chainKeyRef: secret://env/SLUICE_TEST_CHAIN_KEY\n")
	if err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	recs, err := deps.auditFile.Tail(context.Background(), 1)
	if err != nil || len(recs) != 1 {
		t.Fatalf("tail: %v, %d record(s)", err, len(recs))
	}
	if recs[0].HashAlg != audit.HashAlgHMAC {
		t.Errorf("hash_alg of the genesis record = %q, want %q", recs[0].HashAlg, audit.HashAlgHMAC)
	}
	if _, err := buildMCPRuntime(t, "  chainKeyRef: secret://env/SLUICE_TEST_SHORT_KEY\n"); err == nil || !strings.Contains(err.Error(), "32") {
		t.Errorf("start with a short key: %v, want a refusal that names the length", err)
	}
}

// serve and mcp do not start without audit.file.path, and a second
// process on the same audit directory does not start.
func TestBuildRuntime_AuditDirectory(t *testing.T) {
	if !parserbackend.Implemented {
		t.Skip("buildRuntime refuses the stub parser backend (-tags=pure_parser)")
	}
	dir := t.TempDir()
	policyDir := filepath.Join(dir, "policies.d")
	if err := os.MkdirAll(policyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, cfg string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if _, err := buildRuntime(context.Background(), write("none.yaml", "policies:\n  reload: false\n"), policyDir); err == nil || !strings.Contains(err.Error(), "audit.file.path") {
		t.Errorf("start without audit.file.path: %v, want a refusal", err)
	}

	cfg := write("sluice.yaml", "policies:\n  reload: false\naudit:\n  file:\n    path: "+filepath.Join(dir, "audit")+"\n")
	deps, err := buildRuntime(context.Background(), cfg, policyDir)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer deps.Close(context.Background())
	if _, err := buildRuntime(context.Background(), cfg, policyDir); err == nil || !strings.Contains(err.Error(), "another sluice process") {
		t.Errorf("second start on the same audit directory: %v, want a refusal", err)
	}
}
