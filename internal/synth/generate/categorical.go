// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"sort"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const defaultMaxValues = 50

// choice picks one of a fixed list of values by weight.
type choice struct {
	values []any
	cum    []float64
	r      *rng.Stream
}

func (c *choice) Next(RowCtx) (any, error) { return c.values[pickCum(c.cum, c.r)], nil }
func (c *choice) Pool() int64              { return int64(len(c.values)) }

// keptValues returns the kept top values in the stage type with their
// counts.
func keptValues(col Column, g apitypes.SyntheticGenerator, p *profile.Column) ([]any, []float64, error) {
	if len(p.TopValues.Values) == 0 {
		return nil, nil, profile.ErrNoGroups
	}
	st := StageOf(col, g)
	vals := make([]any, len(p.TopValues.Values))
	weights := make([]float64, len(vals))
	for i, v := range p.TopValues.Values {
		sv, err := stageValue(st, v.V)
		if err != nil {
			return nil, nil, err
		}
		vals[i], weights[i] = sv, float64(v.Count)
	}
	return vals, weights, nil
}

func scalar(col Column, g apitypes.SyntheticGenerator) error {
	return fits(col, g, col.Kind() != profile.KindOther)
}

type categorical struct{}

func (categorical) Check(col Column, args Args) error {
	// UUID and ENUM stage as VARCHAR too, but the label would not cast.
	if _, ok := args["otherLabel"]; ok && col.Kind() != profile.KindText {
		return errors.New("otherLabel needs a text column")
	}
	if err := scalar(col, apitypes.GenCategorical); err != nil {
		return err
	}
	return checkBy(col, args)
}

func (categorical) Needs(_ Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartTopValues, MaxValues: args.intOr("maxValues", defaultMaxValues)}
}

func (categorical) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	vals, w, err := keptValues(col, apitypes.GenCategorical, p)
	if err != nil {
		return nil, err
	}
	if label, ok := args["otherLabel"].(string); ok && p.TopValues.Rest > 0 {
		vals = append(vals, label)
		w = append(w, float64(p.TopValues.Rest))
	}
	return &choice{values: vals, cum: cumulative(w), r: s.Values}, nil
}

type uniformCategory struct{}

func (uniformCategory) Check(col Column, _ Args) error {
	return scalar(col, apitypes.GenUniformCategory)
}

func (uniformCategory) Needs(Column, Args) profile.Needs {
	return profile.Needs{Parts: profile.PartTopValues, MaxValues: defaultMaxValues}
}

func (uniformCategory) New(col Column, p *profile.Column, _ Args, s Streams) (ColumnGen, error) {
	vals, w, err := keptValues(col, apitypes.GenUniformCategory, p)
	if err != nil {
		return nil, err
	}
	for i := range w {
		w[i] = 1
	}
	return &choice{values: vals, cum: cumulative(w), r: s.Values}, nil
}

type weighted struct{}

func (weighted) Check(col Column, _ Args) error { return scalar(col, apitypes.GenWeighted) }

func (weighted) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (weighted) New(col Column, _ *profile.Column, args Args, s Streams) (ColumnGen, error) {
	m, _ := args["values"].(map[string]any)
	st := StageOf(col, apitypes.GenWeighted)
	type entry struct {
		key string
		v   any
		w   float64
	}
	entries := make([]entry, 0, len(m))
	for k, w := range m {
		v, err := specValue(col, st, k)
		if err != nil {
			// The key is spec text, not source data, but the message
			// stays the same as for every other cast.
			return nil, errors.New("values: a key " + err.Error())
		}
		entries = append(entries, entry{key: k, v: v, w: w.(float64)})
	}
	// A Go map has no order; sort so the same spec draws the same values.
	sort.Slice(entries, func(i, j int) bool {
		if c := compareStaged(entries[i].v, entries[j].v); c != 0 {
			return c < 0
		}
		return entries[i].key < entries[j].key
	})
	vals := make([]any, len(entries))
	w := make([]float64, len(entries))
	for i, e := range entries {
		vals[i], w[i] = e.v, e.w
	}
	return &choice{values: vals, cum: cumulative(w), r: s.Values}, nil
}

type boolean struct{}

func (boolean) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenBoolean, col.Kind() == profile.KindBool)
}

func (boolean) Needs(_ Column, args Args) profile.Needs {
	if args.isSource("trueRate", true) {
		return profile.Needs{Parts: profile.PartTopValues, MaxValues: 2}
	}
	return profile.Needs{}
}

func (boolean) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	rate, ok := args.num("trueRate")
	if !ok {
		var t, f int64
		for _, v := range p.TopValues.Values {
			if b, _ := v.V.(bool); b {
				t += v.Count
			} else {
				f += v.Count
			}
		}
		if t+f == 0 {
			return nil, profile.ErrNoGroups
		}
		rate = float64(t) / float64(t+f)
	}
	return &boolGen{rate: rate, r: s.Values}, nil
}

type boolGen struct {
	rate float64
	r    *rng.Stream
}

func (g *boolGen) Next(RowCtx) (any, error) { return g.r.Float64() < g.rate, nil }

func (g *boolGen) Pool() int64 {
	if g.rate == 0 || g.rate == 1 {
		return 1
	}
	return 2
}

type shuffleLabels struct{}

func (shuffleLabels) Check(col Column, _ Args) error { return scalar(col, apitypes.GenShuffleLabels) }

func (shuffleLabels) Needs(_ Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartTopValues, MaxValues: args.intOr("maxValues", defaultMaxValues)}
}

func (shuffleLabels) New(col Column, p *profile.Column, _ Args, s Streams) (ColumnGen, error) {
	vals, w, err := keptValues(col, apitypes.GenShuffleLabels, p)
	if err != nil {
		return nil, err
	}
	// The counts stay in their sorted order; the labels are shuffled
	// against them (Fisher-Yates).
	for i := len(vals) - 1; i > 0; i-- {
		j := s.Shuffle.IntN(i + 1)
		vals[i], vals[j] = vals[j], vals[i]
	}
	return &choice{values: vals, cum: cumulative(w), r: s.Values}, nil
}
