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
	// DependsOn lists the parent tables (foreignKey), sorted.
	DependsOn []string `json:"depends_on,omitempty"`
	// ExactMatches is set for a table with privacy.rejectExactMatches.
	ExactMatches *ExactMatchReport `json:"exact_matches,omitempty"`
	// Correlations has one entry per correlated group.
	Correlations []CorrelationReport `json:"correlations,omitempty"`
}

// CorrelationReport is one correlated group of a TableReport. The
// matrices are Spearman rank correlations in Columns order.
type CorrelationReport struct {
	Columns []string `json:"columns"`
	// Cells is B_i per member; pair (a, b) uses a B_a x B_b table.
	Cells []int `json:"cells"`
	// Shrinkage is the share of the identity mixed in so that the
	// Gaussian correlation has a Cholesky factor.
	Shrinkage float64 `json:"shrinkage"`
	// Source is the rho the generator used.
	Source [][]float64 `json:"source"`
	// Synthetic is the rho of the snapshot, nil when not measured.
	Synthetic [][]float64 `json:"synthetic"`
	MaxDiff   float64     `json:"max_diff"`
}

// ExactMatchReport is the rejectExactMatches part of a TableReport. It
// holds counts only.
type ExactMatchReport struct {
	Columns []string `json:"columns"`
	// MatchedRows counts the rows that matched after the main pass,
	// RedrawnRows the rows redrawn in all rounds.
	MatchedRows int64 `json:"matched_rows"`
	RedrawnRows int64 `json:"redrawn_rows"`
	Rounds      int   `json:"rounds"`
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
	Fallbacks int64 `json:"fallbacks,omitempty"`
	// DependsOn lists the columns of the same table this column reads,
	// sorted.
	DependsOn []string `json:"depends_on,omitempty"`
	// By is set for a column with by.
	By *ByReport `json:"by,omitempty"`
	// FanOut is set for a foreignKey column.
	FanOut *FanOutReport `json:"fan_out,omitempty"`
	// Words is the number of kept source vocabulary words, never the
	// words.
	Words    int      `json:"words,omitempty"`
	Warnings []string `json:"warnings"`
}

// FanOutReport is the foreignKey part of a ColumnReport.
type FanOutReport struct {
	// Parent is schema.table.column of the parent key.
	Parent  string `json:"parent"`
	Parents int64  `json:"parents"`
	// Scale is the child rows over the sum of the drawn fan-outs.
	Scale      float64 `json:"scale"`
	OrphanRows int64   `json:"orphan_rows"`
}

// ByReport is the by-group part of a ColumnReport. It holds counts only,
// never a group key.
type ByReport struct {
	Columns       []string `json:"columns"`
	Groups        int      `json:"groups"`
	DroppedGroups int      `json:"dropped_groups"`
	// FallbackRows counts the draws that used the global profile.
	FallbackRows int64 `json:"fallback_rows"`
	// SpikeGroups counts groups with a spike, never their keys.
	SpikeGroups int `json:"spike_groups"`
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
