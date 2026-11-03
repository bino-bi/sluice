// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profile reads aggregates of a source column and filters them,
// so that the result holds no single row's value. Generators read only
// the filtered profile. The profile lives in memory and is never written.
package profile

import "github.com/bino-bi/sluice/pkg/apitypes"

// Part is one profile part. A generator lists the parts it reads.
type Part uint32

// Profile parts. The number parts work on the number axis of the column
// (see Kind.Numeric).
const (
	// PartRange reads the bounds lo' and hi'. Every other number part
	// implies it.
	PartRange Part = 1 << iota
	// PartStats reads mean and standard deviation.
	PartStats
	// PartQuantiles reads the quantile grid.
	PartQuantiles
	// PartLogStats reads mean and standard deviation of ln(x).
	PartLogStats
	// PartBins reads an equal-width histogram with Needs.Bins bins.
	PartBins
	// PartTopValues reads the most common values, at most Needs.MaxValues.
	PartTopValues
	// PartLengths reads the length histogram of a text column.
	PartLengths
	// PartInitials reads the first characters of a text column.
	PartInitials
	// PartCharClasses reads which character classes occur.
	PartCharClasses
	// PartCaseStyles reads the share of upper, lower, title and mixed case.
	PartCaseStyles
	// PartPatterns reads character patterns, at most Needs.MaxPatterns.
	PartPatterns
	// PartPrefixes reads prefixes of Needs.PrefixLength characters.
	PartPrefixes
	// PartWordCounts reads the word count histogram of a text column.
	PartWordCounts
	// PartWeekdays reads the weekday histogram of a date column.
	PartWeekdays
	// PartMonths reads the month histogram of a date column.
	PartMonths
	// PartOffset moves the number parts to the offset axis x - from
	// (days for DATE, seconds for TIMESTAMP), with Needs.From as from.
	// The null rate is then the NULL share of x among the rows where
	// from is not NULL.
	PartOffset
)

const (
	numberParts = PartRange | PartStats | PartQuantiles | PartLogStats | PartBins
	textParts   = PartLengths | PartInitials | PartCharClasses | PartCaseStyles | PartPatterns | PartPrefixes | PartWordCounts
	dateParts   = PartWeekdays | PartMonths
)

// Needs is what a generator reads from the profile. The zero value
// reads nothing.
type Needs struct {
	Parts        Part
	Bins         int
	MaxValues    int
	MaxPatterns  int
	PrefixLength int
	// Unit is the smallest step of the generated values on the number
	// axis, for example 86400 for timestamps cut to days. The jitter
	// moves a bound by at least one unit, so rounding cannot give back
	// the source value. Zero takes the default of the column kind.
	Unit float64
	// From is the source name of the other column of PartOffset.
	From string
	// KeepSign keeps lo' at or above 0 when the source lo is, so a
	// jitter cannot turn "always after" into "sometimes before".
	KeepSign bool
}

// Default privacy settings (concept 5.8).
const (
	DefaultMinGroupSize = 5
	DefaultJitterMin    = 0.01
	DefaultJitterMax    = 0.02
)

// Settings are the effective privacy settings of one column.
type Settings struct {
	MinGroupSize int
	// Clip is nil when clipOutliers is off.
	Clip         *apitypes.ClipOutliers
	JitterOff    bool
	JitterMin    float64
	JitterMax    float64
	SpikeWarning bool
}

// Effective merges privacy levels field by field. levels go from the most
// specific (column) to the least specific (source); nil entries are
// skipped. Unset fields take the defaults.
func Effective(levels ...*apitypes.SyntheticPrivacy) Settings {
	s := Settings{
		MinGroupSize: DefaultMinGroupSize,
		JitterMin:    DefaultJitterMin,
		JitterMax:    DefaultJitterMax,
		SpikeWarning: true,
	}
	var k, clip, jit, spike bool
	for _, p := range levels {
		if p == nil {
			continue
		}
		if !k && p.MinGroupSize != nil {
			s.MinGroupSize, k = *p.MinGroupSize, true
		}
		if !clip && p.ClipOutliers != nil {
			c := *p.ClipOutliers
			s.Clip, clip = &c, true
		}
		if !jit && p.ExtremeJitter != nil {
			j := p.ExtremeJitter
			s.JitterOff, s.JitterMin, s.JitterMax, jit = j.Off, j.Min, j.Max, true
		}
		if !spike && p.SpikeWarning != nil {
			s.SpikeWarning, spike = *p.SpikeWarning, true
		}
	}
	return s
}

// Table names a source table. Catalog is the attached source name.
type Table struct {
	Catalog, Schema, Name string
}

// ColumnInfo is one source column as information_schema lists it.
type ColumnInfo struct {
	Name string
	Type string
}

// Counts holds the base query results of one column.
type Counts struct {
	Rows     int64
	NonNull  int64
	Distinct int64
}

// Column is the filtered profile of one column. Number fields are on the
// number axis of the column kind and already jittered.
type Column struct {
	Counts
	// NullRate is 0 when fewer than k rows are NULL, and 1 when fewer
	// than k rows are not NULL, so a small group does not show.
	NullRate float64

	// Lo and Hi are the jittered bounds lo' and hi'.
	Lo, Hi float64
	// InRange is the number of non-null rows inside the clip range.
	InRange int64
	// Mean and StdDev are taken on the clip range. They are not jittered.
	Mean, StdDev float64
	// Quantiles holds S+1 jittered quantiles at i/S, i = 0..S.
	Quantiles []float64
	// LogMean and LogStdDev are mean and standard deviation of ln(x).
	LogMean, LogStdDev float64
	// Bins holds the number bins after merging, with jittered edges.
	Bins   []Bin
	Spikes []Spike

	TopValues Nominal
	Initials  Nominal
	Patterns  Nominal
	Prefixes  Nominal
	// Weekdays holds dayofweek (0 is Sunday), Months month (1 to 12),
	// both as int64.
	Weekdays Nominal
	Months   Nominal

	// Lengths and WordCounts have one bin per length (Lo == Hi).
	Lengths     []Bin
	WordCounts  []Bin
	CharClasses CharClasses
	// CaseStyles holds the row counts of upper, lower, title and mixed
	// case. A style with fewer than k rows is 0.
	CaseStyles [4]int64

	// Suppressed counts the nominal groups removed for having fewer than
	// k rows. MergedBins counts the ordinal merges.
	Suppressed int64
	MergedBins int
}

// Case style indexes into Column.CaseStyles.
const (
	CaseUpper = iota
	CaseLower
	CaseTitle
	CaseMixed
)

// Value is one kept group of a nominal part.
type Value struct {
	V     any
	Count int64
}

// Nominal is a nominal histogram after the filter. Values is sorted by
// count descending, then by value. Rest is the number of rows of the
// part that are in no kept group.
type Nominal struct {
	Values []Value
	Rest   int64
}

// Bin is one ordinal bin.
type Bin struct {
	Lo, Hi float64
	Count  int64
}

// CharClasses tells which classes occur in at least k rows.
type CharClasses struct {
	Upper, Lower, Digit, Space bool
}

// Spike sides.
const (
	SideLow  = "low"
	SideHigh = "high"
)

// Spike is a value range end with fewer than k rows (4.2 step 8).
type Spike struct {
	Side string
	Rows int64
}
