// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

func makeBenchRecord() *audit.Record {
	return &audit.Record{
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
		EventType: audit.EventQuery,
		QueryID:   "01HXXXXXXXXXXXXXXXXXXXXXX",
		Subject: audit.Subject{
			ID:     "alice@example.com",
			Method: "jwt",
			Issuer: "https://idp.example.com",
			Email:  "alice@example.com",
			Groups: []string{"analytics", "bi"},
		},
		SQLFingerprint: "ae9b3e9951ff4b86",
		Tables:         []string{"pg.public.customers", "pg.public.orders"},
		Decision:       audit.DecisionAllow,
		RowCount:       42,
		SluiceVersion:  "v0.1.0-alpha.1",
		ParserVersion:  "6.1.0",
		PriorHash:      "c5d2bf8d90e7a0a8f4b2e9f8a7e6d5c4b3a2918078695a4b3c2d1e0f1a2b3c4d",
	}
}

// BenchmarkCanonicalJSON is the hot path every audit record passes through.
// Target from plan §17 of the audit spec: ≥ 10 k records/sec throughput.
func BenchmarkCanonicalJSON(b *testing.B) {
	r := makeBenchRecord()
	var buf bytes.Buffer
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		if err := audit.CanonicalJSON(&buf, r); err != nil {
			b.Fatalf("canonical: %v", err)
		}
	}
}

// BenchmarkComputeHash covers both canonical JSON and the sha256 step that
// anchor record chain to its predecessor.
func BenchmarkComputeHash(b *testing.B) {
	r := makeBenchRecord()
	prior := r.PriorHash
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := audit.ComputeHash(prior, r); err != nil {
			b.Fatalf("hash: %v", err)
		}
	}
}
