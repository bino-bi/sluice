// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"time"

	"github.com/bino-bi/sluice/internal/audit"
)

// ReportVersion is the version of the report format.
const ReportVersion = 1

// Build results.
const (
	ResultSucceeded = "succeeded"
	ResultFailed    = "failed"
)

// Report is the build report, written to reports/<build-id>.json. It
// holds aggregates of the source, never a single row's value: the source
// min and max are the quantiles at k/n and 1-k/n, or the clip bounds.
type Report struct {
	Version       int           `json:"version"`
	BuildID       string        `json:"build_id"`
	Name          string        `json:"name"`
	Source        string        `json:"source"`
	Result        string        `json:"result"`
	Error         string        `json:"error"`
	Origin        string        `json:"origin"`
	SpecHash      string        `json:"spec_hash"`
	ContentHash   string        `json:"content_hash"`
	Algorithm     string        `json:"algorithm"`
	SluiceVersion string        `json:"sluice_version"`
	DuckDBVersion string        `json:"duckdb_version"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at"`
	DurationMs    int64         `json:"duration_ms"`
	Tables        []TableReport `json:"tables"`
	Spikes        []SpikeReport `json:"spikes"`
	Warnings      []string      `json:"warnings"`
	// Audit is the audit record of a CLI build, nil for server builds.
	Audit *audit.Record `json:"audit"`
}

// TableReport is one table of a Report.
type TableReport struct {
	Name          string         `json:"name"`
	SourceRows    int64          `json:"source_rows"`
	SyntheticRows int64          `json:"synthetic_rows"`
	Ratio         float64        `json:"ratio"`
	Columns       []ColumnReport `json:"columns"`
}

// ColumnReport is one column of a TableReport.
type ColumnReport struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Generator string    `json:"generator"`
	ChosenBy  string    `json:"chosen_by"`
	Tags      []string  `json:"tags"`
	Source    *Stats    `json:"source"`
	Synthetic *Stats    `json:"synthetic"`
	Distance  *Distance `json:"distance"`
	// SuppressedValues counts the groups removed for having fewer than
	// minGroupSize rows, MergedBins the ordinal merges.
	SuppressedValues int64 `json:"suppressed_values"`
	MergedBins       int   `json:"merged_bins"`
	// Fallbacks counts the values fake keepInitial drew from the whole
	// list.
	Fallbacks int64    `json:"fallbacks,omitempty"`
	Warnings  []string `json:"warnings"`
}

// Stats are the aggregates of one column. Number fields are on the number
// axis of the type (days since 1970 for DATE, seconds for TIMESTAMP and
// TIME) and are nil for other types or too few rows.
type Stats struct {
	NullRate float64  `json:"null_rate"`
	Distinct int64    `json:"distinct"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Median   *float64 `json:"median,omitempty"`
	Mean     *float64 `json:"mean,omitempty"`
}

// Distance compares the synthetic column with the source profile: ks on
// the quantile grid, tv on the kept values.
type Distance struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
}

// SpikeReport is one spike (plan 4.2 step 8).
type SpikeReport struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Side   string `json:"side"`
	Rows   int64  `json:"rows"`
}
