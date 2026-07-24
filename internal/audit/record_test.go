// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func sampleRecord() *audit.Record {
	return &audit.Record{
		Timestamp: time.Date(2026, 4, 20, 9, 30, 0, 0, time.UTC),
		EventType: audit.EventQuery,
		QueryID:   "01JK0000000000000000000000",
		Subject: audit.Subject{
			ID:       "alice@example.com",
			Method:   "jwt",
			Issuer:   "https://idp.example.com/",
			Email:    "alice@example.com",
			Groups:   []string{"analysts", "staff"},
			RemoteIP: "10.0.0.5",
		},
		Origin:               "rest",
		SQLFingerprint:       "fp-before",
		RewrittenFingerprint: "fp-after",
		Tables:               []string{"pg.public.orders"},
		Catalogs:             []string{"pg"},
		PoliciesApplied: []apitypes.AppliedPolicy{
			{Kind: apitypes.KindSQLAccessPolicy, Name: "analysts-orders", Priority: 100},
		},
		Decision:      audit.DecisionAllow,
		RowCount:      42,
		DurationMs:    17,
		SluiceVersion: "0.1.0-dev",
		RemoteIP:      "10.0.0.5",
		PriorHash:     "ffeeddccbbaa",
	}
}

func TestCanonicalJSON_Deterministic(t *testing.T) {
	r := sampleRecord()
	// Shuffle Extras and groups insertion order by rebuilding from scratch.
	r.Extras = map[string]any{"zz": 1, "aa": "two", "mm": true}

	var a, b bytes.Buffer
	if err := audit.CanonicalJSON(&a, r); err != nil {
		t.Fatalf("canonical a: %v", err)
	}
	// Rebuild with a different Extras insertion order.
	r2 := *r
	r2.Extras = map[string]any{"mm": true, "aa": "two", "zz": 1}
	if err := audit.CanonicalJSON(&b, &r2); err != nil {
		t.Fatalf("canonical b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("canonical output non-deterministic:\n a=%s\n b=%s", a.Bytes(), b.Bytes())
	}
}

func TestCanonicalJSON_SortsKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := audit.CanonicalJSON(&buf, sampleRecord()); err != nil {
		t.Fatalf("canonical: %v", err)
	}
	got := buf.String()
	// Top-level keys should be sorted alphabetically. A cheap check: find
	// every `"<key>":` and assert increasing order among top-level keys.
	dec := json.NewDecoder(strings.NewReader(got))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("expected top-level object, got %v", tok)
	}
	var prev string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("json token: %v", err)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("expected key string, got %T=%v", tok, tok)
		}
		if prev != "" && key <= prev {
			t.Fatalf("keys not sorted: %q followed %q", key, prev)
		}
		prev = key
		// Skip the value.
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatalf("decode value: %v", err)
		}
	}
}

func TestCanonicalJSON_OmitsHash(t *testing.T) {
	r := sampleRecord()
	r.Hash = "deadbeef"
	var buf bytes.Buffer
	if err := audit.CanonicalJSON(&buf, r); err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte(`"hash"`)) {
		t.Fatalf("canonical JSON must omit hash field: %s", buf.Bytes())
	}
}

func TestMarshalLine_IncludesHash(t *testing.T) {
	r := sampleRecord()
	r.Hash = "1234abcd"
	line, err := audit.MarshalLine(r)
	if err != nil {
		t.Fatalf("MarshalLine: %v", err)
	}
	if !bytes.HasSuffix(line, []byte("}\n")) {
		t.Fatalf("line must end with }\\n, got %q", line)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &decoded); err != nil {
		t.Fatalf("line is not valid JSON: %v (%s)", err, line)
	}
	if decoded["hash"] != "1234abcd" {
		t.Fatalf("expected hash=1234abcd, got %v", decoded["hash"])
	}
}

func TestComputeHash_StableAndChains(t *testing.T) {
	r := sampleRecord()
	r.PriorHash = "prior-1"

	h1, err := audit.ComputeHash("prior-1", r)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	h2, err := audit.ComputeHash("prior-1", r)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %s vs %s", h1, h2)
	}

	// Flipping any input byte must change the hash.
	r.RowCount = 43
	h3, err := audit.ComputeHash("prior-1", r)
	if err != nil {
		t.Fatalf("hash3: %v", err)
	}
	if h3 == h1 {
		t.Fatalf("hash must change when record changes")
	}

	// Different prior hash → different output.
	r.RowCount = 42
	h4, err := audit.ComputeHash("prior-2", r)
	if err != nil {
		t.Fatalf("hash4: %v", err)
	}
	if h4 == h1 {
		t.Fatalf("hash must change when prior changes")
	}
}

func TestGenesisPriorHash(t *testing.T) {
	a := audit.GenesisPriorHash([]byte("seed-1"))
	b := audit.GenesisPriorHash([]byte("seed-1"))
	if a != b {
		t.Fatalf("genesis hash not deterministic: %s vs %s", a, b)
	}
	c := audit.GenesisPriorHash([]byte("seed-2"))
	if a == c {
		t.Fatalf("genesis hash must differ between seeds")
	}
	if len(a) != 64 { // sha256 hex
		t.Fatalf("genesis hash wrong length: %d", len(a))
	}
}

func TestCanonicalJSON_ClientMetaDeterministic(t *testing.T) {
	r := sampleRecord()
	r.ClientMeta = map[string]string{"zz": "1", "aa": "two", "mm": "x"}

	var a, b bytes.Buffer
	if err := audit.CanonicalJSON(&a, r); err != nil {
		t.Fatalf("canonical a: %v", err)
	}
	r2 := *r
	r2.ClientMeta = map[string]string{"mm": "x", "aa": "two", "zz": "1"}
	if err := audit.CanonicalJSON(&b, &r2); err != nil {
		t.Fatalf("canonical b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("client_meta canonicalization non-deterministic:\n a=%s\n b=%s", a.Bytes(), b.Bytes())
	}
	if !strings.Contains(a.String(), `"client_meta":{"aa":"two","mm":"x","zz":"1"}`) {
		t.Fatalf("client_meta not sorted/present: %s", a.String())
	}
}

func TestCanonicalJSON_EmptyClientMetaOmitted(t *testing.T) {
	// A record with a nil map and one with an empty map must canonicalize
	// byte-identically to a record that never had the field — existing
	// chains must keep verifying.
	base := sampleRecord()
	var want bytes.Buffer
	if err := audit.CanonicalJSON(&want, base); err != nil {
		t.Fatalf("canonical base: %v", err)
	}
	withEmpty := *base
	withEmpty.ClientMeta = map[string]string{}
	var got bytes.Buffer
	if err := audit.CanonicalJSON(&got, &withEmpty); err != nil {
		t.Fatalf("canonical empty: %v", err)
	}
	if !bytes.Equal(want.Bytes(), got.Bytes()) {
		t.Fatalf("empty client_meta changed canonical bytes:\n want=%s\n got=%s", want.Bytes(), got.Bytes())
	}
}

func TestComputeHash_ChainsWithClientMeta(t *testing.T) {
	r1 := sampleRecord()
	h1, err := audit.ComputeHash("prior-1", r1)
	if err != nil {
		t.Fatalf("hash r1: %v", err)
	}
	r2 := sampleRecord()
	r2.ClientMeta = map[string]string{"dashboard": "revenue-daily"}
	r2.PriorHash = h1
	h2, err := audit.ComputeHash(h1, r2)
	if err != nil {
		t.Fatalf("hash r2: %v", err)
	}
	if h2 == "" || h2 == h1 {
		t.Fatalf("chained hash with client_meta: got %q", h2)
	}
	// Same record without client_meta must hash differently.
	r3 := sampleRecord()
	r3.PriorHash = h1
	h3, err := audit.ComputeHash(h1, r3)
	if err != nil {
		t.Fatalf("hash r3: %v", err)
	}
	if h3 == h2 {
		t.Fatal("client_meta not included in hash input")
	}
}
