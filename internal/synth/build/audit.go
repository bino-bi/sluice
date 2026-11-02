// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"github.com/bino-bi/sluice/internal/audit"
)

// AuditRecord returns the synthetic-build record of a report for phase
// start or end. The caller sets Origin, Subject and RemoteIP. The record
// holds row counts and hashes only, never a value of the source.
func AuditRecord(r *Report, phase string) *audit.Record {
	sourceRows := make(map[string]int64, len(r.Tables))
	syntheticRows := make(map[string]int64, len(r.Tables))
	for _, t := range r.Tables {
		sourceRows[t.Name] = t.SourceRows
		syntheticRows[t.Name] = t.SyntheticRows
	}
	rec := &audit.Record{
		Timestamp:     r.FinishedAt,
		EventType:     audit.EventSyntheticBuild,
		Origin:        r.Origin,
		Catalogs:      []string{r.Source, r.Name},
		Decision:      audit.DecisionAllow,
		DurationMs:    r.DurationMs,
		SluiceVersion: r.SluiceVersion,
		DuckDBVersion: r.DuckDBVersion,
		Message:       "synthetic build",
		Extras: map[string]any{
			"phase":          phase,
			"build_id":       r.BuildID,
			"spec_hash":      r.SpecHash,
			"content_hash":   r.ContentHash,
			"source_rows":    sourceRows,
			"synthetic_rows": syntheticRows,
			"result":         r.Result,
		},
	}
	if r.Result == ResultFailed {
		rec.Decision = audit.DecisionError
		rec.Message = "synthetic build: " + r.Error
	}
	return rec
}
