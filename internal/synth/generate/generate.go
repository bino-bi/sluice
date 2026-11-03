// SPDX-License-Identifier: AGPL-3.0-or-later

// Package generate draws the values of synthetic columns. A generator
// reads only the filtered profile (package profile) and its random
// streams, never a source row.
package generate

import (
	"errors"
	"fmt"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Args are the args of one generator as they arrive from JSON: numbers
// are float64, lists []any, objects map[string]any. The decoder has
// checked them (apitypes).
type Args map[string]any

// Column is one source column as the generators see it.
type Column struct {
	Name string
	// Type is the information_schema data_type.
	Type string
	// Rows is the row count of the synthetic table.
	Rows int64
	// Cast converts a value from the spec (constant, weighted keys) to
	// the column type. The builder runs CAST(? AS <type>) in DuckDB.
	Cast func(value string) (any, error)
	// Siblings are the columns of the same table by lower-case name.
	Siblings map[string]Sibling
	// Keys are the parent keys of a foreignKey column. Nil otherwise.
	Keys *KeySet
}

// KeySet holds the staged values of a parent key column in row order,
// without NULLs.
type KeySet struct {
	Values []any
	Stage  Stage
}

// Sibling is a column of the same table, for generators that read other
// columns of the row.
type Sibling struct {
	// Index is the ordinal position, the index into RowCtx.Values.
	Index int
	// Name is the name as in the source.
	Name string
	// Type is the information_schema data_type.
	Type string
}

// Kind returns the profile kind of the column type.
func (c Column) Kind() profile.Kind { return profile.KindOf(c.Type) }

// RowCtx is the row being generated. Values holds the values drawn for
// this row by ordinal position. The builder fills the columns a
// generator reads before it calls the generator (Order).
type RowCtx struct {
	Index  int64
	Values []any
}

// Streams are the random streams of one column (rng purposes).
type Streams struct {
	Values, Nulls, Shuffle *rng.Stream
	// FanOut and Orphans are read by foreignKey only.
	FanOut, Orphans *rng.Stream
	// Copula is the joint draw of a correlated column, nil for others.
	Copula *CopulaSlot
}

// Generator is one generator type.
type Generator interface {
	// Check validates args against the column type before any profile query.
	Check(col Column, args Args) error
	// Needs lists the profile parts this generator reads. The zero value
	// means none.
	Needs(col Column, args Args) profile.Needs
	// New returns a value source for one column. The values have the Go
	// type of the column's Stage.
	New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error)
}

// ColumnGen draws the values of one column.
type ColumnGen interface {
	// Next returns the next value.
	Next(row RowCtx) (any, error)
	// Pool returns the number of distinct values it can draw, or -1.
	Pool() int64
}

// FallbackCounter is a ColumnGen that sometimes cannot honor an arg and
// falls back (fake with keepInitial). The report shows the count.
type FallbackCounter interface {
	Fallbacks() int64
}

var registry = map[apitypes.SyntheticGenerator]Generator{
	apitypes.GenDistribution:     distribution{},
	apitypes.GenUniform:          uniform{},
	apitypes.GenNormal:           normal{},
	apitypes.GenLognormal:        lognormal{},
	apitypes.GenHistogram:        histogram{},
	apitypes.GenBucket:           bucket{},
	apitypes.GenConstant:         constant{},
	apitypes.GenSequence:         sequence{},
	apitypes.GenRange:            rangeGen{},
	apitypes.GenCategorical:      categorical{},
	apitypes.GenUniformCategory:  uniformCategory{},
	apitypes.GenWeighted:         weighted{},
	apitypes.GenBoolean:          boolean{},
	apitypes.GenShuffleLabels:    shuffleLabels{},
	apitypes.GenRandomString:     randomString{},
	apitypes.GenPattern:          pattern{},
	apitypes.GenRegex:            regex{},
	apitypes.GenFake:             fake{},
	apitypes.GenUUID:             uuidGen{},
	apitypes.GenPrefixKeep:       prefixKeep{},
	apitypes.GenLoremText:        loremText{},
	apitypes.GenRandomText:       randomText{},
	apitypes.GenRedact:           redact{},
	apitypes.GenDateDistribution: dateDistribution{},
	apitypes.GenDateRange:        dateRange{},
	apitypes.GenNull:             null{},
	apitypes.GenTemplate:         template{},
	apitypes.GenDateOffset:       dateOffset{},
	apitypes.GenExpression:       expression{},
	apitypes.GenForeignKey:       foreignKey{},
	apitypes.GenSourceVocabulary: sourceVocabulary{},
	apitypes.GenSentenceShape:    sentenceShape{},
	apitypes.GenCorrelated:       correlated{},
}

// Lookup returns the generator of a name.
func Lookup(name apitypes.SyntheticGenerator) (Generator, bool) {
	g, ok := registry[name]
	return g, ok
}

// ErrType is returned by Check when a generator does not fit the column
// type.
var ErrType = errors.New("generator does not fit the column type")

// maxUniqueRetries is how often a duplicate is drawn again in a row
// before a unique column fails.
const maxUniqueRetries = 1000

// New returns the value source of one column: the generator with the
// shared args nullRate and unique applied. p may be nil for a generator
// without needs; the null rate is then 0 unless set in args.
func New(name apitypes.SyntheticGenerator, col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("column %s: unknown generator %q", col.Name, name)
	}
	if p == nil {
		p = &profile.Column{}
	}
	inner, err := g.New(col, p, args, s)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", col.Name, err)
	}
	return wrap(inner, col, p.NullRate, args, s)
}

// wrap applies the shared args nullRate and unique to inner. rate is the
// profile null rate, used unless args set nullRate.
func wrap(inner ColumnGen, col Column, rate float64, args Args, s Streams) (ColumnGen, error) {
	if v, ok := args["nullRate"].(float64); ok {
		rate = v
	}
	w := &wrapped{inner: inner, rate: rate, nulls: s.Nulls, name: col.Name}
	if unique, _ := args["unique"].(bool); unique {
		if pool := inner.Pool(); pool >= 0 && rate == 0 && pool < col.Rows {
			return nil, fmt.Errorf("column %s: unique needs %d values, the generator has only %d", col.Name, col.Rows, pool)
		}
		w.seen = map[any]struct{}{}
	}
	return w, nil
}

// wrapped applies nullRate and unique.
type wrapped struct {
	inner ColumnGen
	rate  float64
	nulls *rng.Stream
	seen  map[any]struct{}
	name  string
}

func (w *wrapped) Next(row RowCtx) (any, error) {
	// The null draw happens on every row, so the null pattern does not
	// depend on the values.
	if w.nulls.Float64() < w.rate {
		return nil, nil
	}
	for range maxUniqueRetries {
		v, err := w.inner.Next(row)
		if err != nil || v == nil || w.seen == nil {
			return v, err
		}
		if _, dup := w.seen[v]; !dup {
			w.seen[v] = struct{}{}
			return v, nil
		}
	}
	return nil, fmt.Errorf("column %s: cannot draw unique values, %d distinct values reached", w.name, len(w.seen))
}

func (w *wrapped) Pool() int64 { return w.inner.Pool() }

// Fallbacks passes the inner count through.
func (w *wrapped) Fallbacks() int64 {
	if f, ok := w.inner.(FallbackCounter); ok {
		return f.Fallbacks()
	}
	return 0
}

// ByRows passes the inner count through.
func (w *wrapped) ByRows() (int64, int64) {
	if b, ok := w.inner.(ByCounter); ok {
		return b.ByRows()
	}
	return 0, 0
}

// FanOutStats passes the inner counts through, nil for other generators.
func (w *wrapped) FanOutStats() *FanOutStats {
	if f, ok := w.inner.(FanOuter); ok {
		return f.FanOutStats()
	}
	return nil
}

// pickCum draws an index from cumulative weights cum (ascending, last is
// the total) by a binary search on U() * total.
func pickCum(cum []float64, r *rng.Stream) int {
	x := float64(r.Float64() * cum[len(cum)-1])
	lo, hi := 0, len(cum)-1
	for lo < hi {
		m := (lo + hi) / 2
		if cum[m] > x {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return lo
}

// cumulative returns the running sums of weights.
func cumulative(weights []float64) []float64 {
	out := make([]float64, len(weights))
	var t float64
	for i, w := range weights {
		t += w
		out[i] = t
	}
	return out
}

// typeError names the column and its type, not a value.
func typeError(col Column, g apitypes.SyntheticGenerator) error {
	return fmt.Errorf("%w: %s on %s", ErrType, g, profile.DisplayType(col.Type))
}
