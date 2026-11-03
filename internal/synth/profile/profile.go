// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profile reads aggregates of a source column and filters them,
// so that the result holds no single row's value. Generators read only
// the filtered profile. The profile lives in memory and is never written.
package profile

import (
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

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
	// PartFanOut reads the number of rows per key of the parent column
	// Needs.Parent, as an ordinal histogram over the parent keys, and the
	// share of rows whose key has no parent.
	PartFanOut
	// PartVocabulary reads the words (letter runs, lower case) that occur
	// Needs.MinWordCount times in k rows, at most Needs.MaxWords.
	PartVocabulary
	// PartSentences reads the sentence count histogram of a text column.
	PartSentences
	// PartSentenceWords reads the words per sentence histogram. Its bins
	// count sentences; Bin.Rows counts the rows they come from.
	PartSentenceWords
	// PartTerminators reads the sentence ends ., ! and ?.
	PartTerminators
	// PartAges reads the age in whole years at Needs.AsOf, or at hi' of
	// PartRange without it. The generator reads PartRange with it.
	PartAges
	// PartTimes reads the time of day in slots of TimeSlot seconds.
	PartTimes
)

// TimeSlot is the width of one PartTimes slot in seconds.
const TimeSlot = 900

const (
	numberParts = PartRange | PartStats | PartQuantiles | PartLogStats | PartBins
	textParts   = PartLengths | PartInitials | PartCharClasses | PartCaseStyles | PartPatterns | PartPrefixes | PartWordCounts
	wordParts   = PartVocabulary | PartSentences | PartSentenceWords | PartTerminators
	dateParts   = PartWeekdays | PartMonths | PartAges
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
	// Parent is the parent key column of PartFanOut, in the same catalog.
	Parent *Ref
	// MinWordCount and MaxWords are the word threshold and cap of
	// PartVocabulary.
	MinWordCount int
	MaxWords     int
	// AsOf is the reference date of PartAges. Nil takes hi'.
	AsOf *time.Time
}

// ReadsNumbers reports whether needs reads a number part.
func (n Needs) ReadsNumbers() bool { return n.Parts&numberParts != 0 }

// Ref names a column of another table of the same catalog.
type Ref struct {
	Schema, Table, Column string
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
	// Bounds is the public range of the column. Only with noise.
	Bounds *apitypes.SyntheticBounds
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
	var k, clip, jit, spike, bounds bool
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
		if !bounds && p.Bounds != nil {
			b := *p.Bounds
			s.Bounds, bounds = &b, true
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
	// Ages holds the age in whole years at AsOf, Times the slot of the
	// time of day (0 to 86400/TimeSlot - 1), both as int64.
	Ages  Nominal
	AsOf  time.Time
	Times Nominal

	// Lengths and WordCounts have one bin per length (Lo == Hi).
	Lengths     []Bin
	WordCounts  []Bin
	CharClasses CharClasses
	// CaseStyles holds the row counts of upper, lower, title and mixed
	// case. A style with fewer than k rows is 0.
	CaseStyles [4]int64

	// FanOut has one bin per number of rows per parent key (Lo == Hi);
	// Count is the number of parent keys. OrphanRate is the share of
	// non-null rows whose key has no parent, 0 below k such rows.
	FanOut     []Bin
	OrphanRate float64

	// Vocabulary holds the kept words with their occurrence counts.
	Vocabulary Nominal
	// Sentences has one bin per sentence count, SentenceWords one bin per
	// word count of a sentence (both Lo == Hi). Terminators holds ".",
	// "!" and "?" by sentence count; it is empty when none is kept.
	Sentences     []Bin
	SentenceWords []Bin
	Terminators   Nominal

	// Suppressed counts the nominal groups removed for having fewer than
	// k rows. MergedBins counts the ordinal merges.
	Suppressed int64
	MergedBins int

	// axisQ is the quantile grid on the source axis before jitter, kept
	// for CellEdges. It never leaves the package.
	axisQ []float64
}

// maxCells caps the correlation cells per axis.
const maxCells = 20

// Cells is the number of correlation cells per axis: min(20, S), with S
// the steps of the quantile grid. It is 0 without a grid.
func (c *Column) Cells() int {
	if len(c.axisQ) < 2 {
		return 0
	}
	return min(maxCells, len(c.axisQ)-1)
}

// CellEdges returns the n-1 inner edges at j/n on the source axis,
// interpolated on the quantile grid. With n <= S a cell spans at least one
// grid step, so it holds about k rows or more. Ties break this: a tied
// value on an edge can leave a cell with fewer than k rows. Only rho leaves
// the package. The edges are source aggregates: the builder passes them
// back to Correlation, and they must not reach a log, an error or the
// report.
func (c *Column) CellEdges(n int) []float64 {
	q := c.axisQ
	s := len(q) - 1
	if s < 1 || n < 1 {
		return nil
	}
	edges := make([]float64, n-1)
	for j := range edges {
		x := float64(float64(j+1) / float64(n) * float64(s))
		i := min(int(x), s-1)
		t := x - float64(i)
		edges[j] = q[i] + float64(t*(q[i+1]-q[i]))
	}
	return edges
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
	// Rows is the number of source rows in the bin when it differs from
	// Count (one row can hold many sentences). 0 means the same as Count.
	Rows int64
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
