// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/audit"
)

var (
	chainKey = []byte("0123456789abcdef0123456789abcdef")
	otherKey = []byte("fedcba9876543210fedcba9876543210")
)

// writeChain appends n query records to dir with key (nil: plain chain).
func writeChain(t *testing.T, dir string, key []byte, n int) error {
	t.Helper()
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("file sink: %v", err)
	}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{fs}, GenesisSeed: []byte("keyed-seed"), ChainKey: key,
	})
	if err != nil {
		_ = fs.Close(context.Background())
		return err
	}
	for range n {
		if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow, SQLSample: "SELECT 1"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	return nil
}

// readRecords returns the records of the single file in dir and its path.
func readRecords(t *testing.T, dir string) ([]*audit.Record, string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("files = %v, want one", files)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []*audit.Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r audit.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, &r)
	}
	return out, files[0]
}

func writeRecords(t *testing.T, path string, recs []*audit.Record) {
	t.Helper()
	var b strings.Builder
	for _, r := range recs {
		line, err := audit.MarshalLine(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A log written before the chain had a key verifies as before. The files
// were written with the code of v0.1.0-rc.2 plus two commits (3e4b483).
func TestVerify_LegacyChain(t *testing.T) {
	const anchor = "70881bcaf9d44ab08866cc1655f1ceaeb0a27f67caac8d8d55934272826e1aee"
	{
		rep, err := audit.Verify("testdata/legacy-chain", anchor, nil)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if rep.Records != 5 || rep.Files != 2 || rep.Keyed != 0 || !strings.HasPrefix(rep.LastHash, "2eab1fecaaef47e2") {
			t.Errorf("report = %+v", rep)
		}
	}
}

func TestKeyedChain_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, chainKey, 3); err != nil {
		t.Fatal(err)
	}
	rep, err := audit.Verify(dir, audit.GenesisPriorHash([]byte("keyed-seed")), chainKey)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Records != 4 || rep.Keyed != 4 {
		t.Errorf("report = %+v, want 4 keyed records", rep)
	}
	// Without the key the chain is neither intact nor broken.
	if _, err := audit.Verify(dir, "", nil); !errors.Is(err, audit.ErrKeyRequired) || errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify without key: %v, want ErrKeyRequired", err)
	}
	if _, err := audit.Verify(dir, "", otherKey); !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify with another key: %v, want a broken chain", err)
	}
}

// The plain chain does not detect a changed record when every later hash
// is computed again. The keyed chain does: the writer has no key.
func TestKeyedChain_DetectsARecomputedChain(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  []byte
	}{{"plain", nil}, {"keyed", chainKey}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeChain(t, dir, tc.key, 3); err != nil {
				t.Fatal(err)
			}
			recs, path := readRecords(t, dir)
			// Change one record, then compute the plain hashes again, as
			// a writer without the key can.
			recs[1].SQLSample = "SELECT secret FROM vault"
			for i := 1; i < len(recs); i++ {
				recs[i].PriorHash = recs[i-1].Hash
				h, err := audit.ComputeHash(recs[i].PriorHash, recs[i])
				if err != nil {
					t.Fatal(err)
				}
				recs[i].Hash = h
			}
			writeRecords(t, path, recs)
			_, err := audit.Verify(dir, "", tc.key)
			if tc.key == nil && err != nil {
				t.Errorf("plain chain: %v (the plain chain cannot see this change)", err)
			}
			if tc.key != nil && !errors.Is(err, audit.ErrChainBroken) {
				t.Errorf("keyed chain: %v, want a broken chain", err)
			}
		})
	}
}

// A writer without the key could also drop hash_alg and continue as a
// plain chain. A plain record after a keyed one breaks the chain.
func TestKeyedChain_PlainRecordAfterKeyed(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, chainKey, 2); err != nil {
		t.Fatal(err)
	}
	recs, path := readRecords(t, dir)
	last := recs[len(recs)-1]
	forged := &audit.Record{EventType: audit.EventQuery, Timestamp: last.Timestamp, PriorHash: last.Hash, SQLSample: "forged"}
	h, err := audit.ComputeHash(forged.PriorHash, forged)
	if err != nil {
		t.Fatal(err)
	}
	forged.Hash = h
	writeRecords(t, path, append(recs, forged))
	if _, err := audit.Verify(dir, "", chainKey); !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify: %v, want a broken chain", err)
	}
}

// A plain chain may get a key. After that the key must stay.
func TestKeyedChain_SwitchAndStartGuard(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, nil, 2); err != nil {
		t.Fatal(err)
	}
	if err := writeChain(t, dir, chainKey, 2); err != nil {
		t.Fatalf("switch to a key: %v", err)
	}
	rep, err := audit.Verify(dir, audit.GenesisPriorHash([]byte("keyed-seed")), chainKey)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Records != 5 || rep.Keyed != 2 {
		t.Errorf("report = %+v, want 5 records, 2 keyed", rep)
	}
	if err := writeChain(t, dir, nil, 1); err == nil || !strings.Contains(err.Error(), "audit.chainKeyRef") {
		t.Errorf("start without the key: %v, want a refusal that names audit.chainKeyRef", err)
	}
	if err := writeChain(t, dir, otherKey, 1); err == nil {
		t.Error("start with another key was accepted")
	}
	if err := writeChain(t, dir, chainKey, 1); err != nil {
		t.Errorf("start with the right key: %v", err)
	}
	if err := writeChain(t, t.TempDir(), []byte("short"), 1); err == nil {
		t.Error("a key under 32 bytes was accepted")
	}
}

// An emptied directory must not verify.
func TestVerify_NoRecordIsABrokenChain(t *testing.T) {
	if _, err := audit.Verify(t.TempDir(), "", nil); !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify of an empty directory: %v, want a broken chain", err)
	}
}

// A writer without the key can strip hash_alg from every record and
// compute a plain chain. With a key, verify must not call that intact.
func TestKeyedChain_FullDowngradeFailsWithAKey(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, chainKey, 3); err != nil {
		t.Fatal(err)
	}
	recs, path := readRecords(t, dir)
	recs[1].SQLSample = "SELECT secret FROM vault"
	for i, r := range recs {
		r.HashAlg = ""
		if i > 0 {
			r.PriorHash = recs[i-1].Hash
		}
		h, err := audit.ComputeHash(r.PriorHash, r)
		if err != nil {
			t.Fatal(err)
		}
		r.Hash = h
	}
	writeRecords(t, path, recs)
	if _, err := audit.Verify(dir, "", chainKey); !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify with the key: %v, want a broken chain", err)
	}
	// Without a key it is a plain chain like any other.
	if _, err := audit.Verify(dir, "", nil); err != nil {
		t.Errorf("verify without a key: %v", err)
	}
}

func TestVerify_UnknownHashAlg(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, chainKey, 1); err != nil {
		t.Fatal(err)
	}
	recs, path := readRecords(t, dir)
	recs[1].HashAlg = "md5"
	writeRecords(t, path, recs)
	if _, err := audit.Verify(dir, "", chainKey); !errors.Is(err, audit.ErrChainBroken) {
		t.Errorf("verify: %v, want a broken chain", err)
	}
}

// The first refused start leaves an empty file for the new day. The
// second start must still find the keyed record in the file before it.
func TestKeyedChain_StartGuardSurvivesAnEmptyNewestFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeChain(t, dir, chainKey, 2); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit-2999-01-01.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := writeChain(t, dir, nil, 1); err == nil {
			t.Fatalf("start %d without the key was accepted", i+1)
		}
	}
	// With the key the chain goes on, it does not start a new genesis.
	if err := writeChain(t, dir, chainKey, 1); err != nil {
		t.Fatalf("start with the key: %v", err)
	}
	if _, err := audit.Verify(dir, audit.GenesisPriorHash([]byte("keyed-seed")), chainKey); err != nil {
		t.Errorf("verify: %v", err)
	}
}
