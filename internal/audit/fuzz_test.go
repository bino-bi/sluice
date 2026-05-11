// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

// FuzzRecordCanonical asserts that canonical JSON is stable across the
// record fields the fuzzer controls, and that ComputeHash + MarshalLine
// never panic on arbitrary input. The invariant we guard: serialising
// the same Record twice produces byte-identical output, so the audit
// chain remains replay-deterministic.
func FuzzRecordCanonical(f *testing.F) {
	seeds := [][]string{
		{"q-1", "alice", "jwt", "pg.public.orders", "allow", "ERR_NONE", "0"},
		{"", "", "none", "", "deny", "ACL_DENIED", "0"},
		{"q-42", "bob@example.com", "api_key", "warehouse.public.pii", "reject", "ACL_REJECTED", "1"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2], s[3], s[4], s[5], s[6])
	}

	f.Fuzz(func(t *testing.T, queryID, subject, method, table, decision, errCode, priorHash string) {
		r := &audit.Record{
			Timestamp: time.Unix(1_700_000_000, 0).UTC(),
			EventType: audit.EventQuery,
			QueryID:   queryID,
			Subject: audit.Subject{
				ID:     subject,
				Method: method,
			},
			Tables:    []string{table},
			Decision:  decision,
			ErrorCode: errCode,
			PriorHash: priorHash,
		}

		var first bytes.Buffer
		if err := audit.CanonicalJSON(&first, r); err != nil {
			t.Fatalf("canonicalise: %v", err)
		}
		var second bytes.Buffer
		if err := audit.CanonicalJSON(&second, r); err != nil {
			t.Fatalf("canonicalise 2: %v", err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatalf("non-deterministic canonical JSON:\n  a: %s\n  b: %s", first.String(), second.String())
		}

		// ComputeHash must be stable for the same (prior, record) pair.
		h1, err := audit.ComputeHash(r.PriorHash, r)
		if err != nil {
			t.Fatalf("ComputeHash: %v", err)
		}
		h2, err := audit.ComputeHash(r.PriorHash, r)
		if err != nil {
			t.Fatalf("ComputeHash (2): %v", err)
		}
		if h1 != h2 {
			t.Fatalf("ComputeHash not deterministic: %s vs %s", h1, h2)
		}

		// MarshalLine exercises the `}` splice and should never return nil
		// bytes or a trailing-newline violation.
		r.Hash = h1
		line, err := audit.MarshalLine(r)
		if err != nil {
			t.Fatalf("MarshalLine: %v", err)
		}
		if n := len(line); n < 2 || line[n-1] != '\n' {
			t.Fatalf("MarshalLine: missing trailing newline (%q)", line)
		}
	})
}
