// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

var errNoGrid = errors.New("profile has no quantile grid")

func isNumber(k profile.Kind) bool { return k == profile.KindInteger || k == profile.KindFloat }

// fits returns a type error unless ok.
func fits(col Column, g apitypes.SyntheticGenerator, ok bool) error {
	if !ok {
		return typeError(col, g)
	}
	return nil
}

// numGen draws on the number axis and writes the stage type.
type numGen struct {
	st     Stage
	lo, hi float64
	round  rounding
	r      *rng.Stream
	draw   func() float64
}

func (g *numGen) Next(RowCtx) (any, error) {
	return fromAxis(g.st, g.round.fit(g.draw(), g.lo, g.hi)), nil
}

func (g *numGen) Pool() int64 { return g.round.pool(g.lo, g.hi) }

func newNumGen(col Column, g apitypes.SyntheticGenerator, p *profile.Column, args Args, s Streams) *numGen {
	lo, hi := clampToType(col, p.Lo, p.Hi)
	return &numGen{st: StageOf(col, g), lo: lo, hi: hi, round: roundingOf(col, args), r: s.Values}
}

// clampToType limits the jittered profile bounds to the values the
// column type holds. The jitter can move them past a type limit (below 0
// on UINTEGER, past 99.99 on DECIMAL(4,2), past midnight on TIME), and
// the final CAST would then fail or wrap.
func clampToType(col Column, lo, hi float64) (float64, float64) {
	tlo, thi := typeRange(col)
	return min(max(lo, tlo), thi), max(min(hi, thi), tlo)
}

// typeRange returns the number axis range of the column type.
func typeRange(col Column) (float64, float64) {
	t := strings.ToUpper(strings.ReplaceAll(col.Type, " ", ""))
	// Nextafter: float64(MaxInt64) is 2^63, which does not fit.
	switch t {
	case "TINYINT":
		return math.MinInt8, math.MaxInt8
	case "SMALLINT":
		return math.MinInt16, math.MaxInt16
	case "INTEGER":
		return math.MinInt32, math.MaxInt32
	case "BIGINT":
		return math.MinInt64, math.Nextafter(math.MaxInt64, 0)
	case "UTINYINT":
		return 0, math.MaxUint8
	case "USMALLINT":
		return 0, math.MaxUint16
	case "UINTEGER":
		return 0, math.MaxUint32
	case "UBIGINT":
		return 0, math.Nextafter(math.MaxUint64, 0)
	case "UHUGEINT":
		return 0, math.Inf(1)
	case "TIME":
		return 0, secondsPerDay - 1e-6
	}
	var prec, scale int
	if n, _ := fmt.Sscanf(t, "DECIMAL(%d,%d)", &prec, &scale); n == 2 {
		m := math.Pow10(prec-scale) - math.Pow10(-scale)
		return -m, m
	}
	return math.Inf(-1), math.Inf(1)
}

// gridValue draws from the quantile grid q (5.1 distribution).
func gridValue(q []float64, u float64, smooth bool) float64 {
	steps := len(q) - 1
	x := float64(u * float64(steps))
	i := min(int(x), steps-1)
	t := x - float64(i)
	if smooth {
		return q[i] + float64(t*(q[i+1]-q[i]))
	}
	if t < 0.5 {
		return q[i]
	}
	return q[i+1]
}

type distribution struct{}

func (distribution) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenDistribution, isNumber(k) || k == profile.KindTime)
}

func (distribution) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartQuantiles, Unit: roundingOf(col, args).unit()}
}

func (distribution) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if len(p.Quantiles) < 2 {
		return nil, errNoGrid
	}
	g := newNumGen(col, apitypes.GenDistribution, p, args, s)
	smooth := args.boolOr("smooth", true)
	g.draw = func() float64 { return gridValue(p.Quantiles, g.r.Float64(), smooth) }
	return g, nil
}

type uniform struct{}

func (uniform) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenUniform, isNumber(k) || k == profile.KindTime)
}

func (uniform) Needs(col Column, args Args) profile.Needs {
	_, okMin := args.num("min")
	_, okMax := args.num("max")
	if okMin && okMax {
		return profile.Needs{}
	}
	return profile.Needs{Parts: profile.PartRange, Unit: roundingOf(col, args).unit()}
}

func (uniform) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := &numGen{st: StageOf(col, apitypes.GenUniform), round: roundingOf(col, Args{}), r: s.Values}
	g.lo, g.hi = clampToType(col, p.Lo, p.Hi)
	if v, ok := args.num("min"); ok {
		g.lo = v
	}
	if v, ok := args.num("max"); ok {
		g.hi = v
	}
	if g.lo > g.hi {
		return nil, errors.New("min is above max")
	}
	g.draw = func() float64 { return g.lo + float64(g.r.Float64()*(g.hi-g.lo)) }
	return g, nil
}

// maxTruncateDraws caps the redraws of a truncated normal value.
const maxTruncateDraws = 64

type normal struct{}

func (normal) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenNormal, isNumber(col.Kind()))
}

func (normal) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartStats, Unit: roundingOf(col, args).unit()}
}

func (normal) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := newNumGen(col, apitypes.GenNormal, p, args, s)
	g.draw = truncated(g, args.boolOr("truncate", true), func() float64 {
		return p.Mean + float64(p.StdDev*g.r.Norm())
	})
	return g, nil
}

type lognormal struct{}

func (lognormal) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenLognormal, isNumber(col.Kind()))
}

func (lognormal) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartLogStats, Unit: roundingOf(col, args).unit()}
}

func (lognormal) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := newNumGen(col, apitypes.GenLognormal, p, args, s)
	g.draw = truncated(g, args.boolOr("truncate", true), func() float64 {
		return math.Exp(p.LogMean + float64(p.LogStdDev*g.r.Norm()))
	})
	return g, nil
}

// truncated draws again while the value is outside [lo, hi], at most
// maxTruncateDraws times. fit clamps the last one.
func truncated(g *numGen, on bool, draw func() float64) func() float64 {
	if !on {
		return draw
	}
	return func() float64 {
		var v float64
		for range maxTruncateDraws {
			if v = draw(); v >= g.lo && v <= g.hi {
				break
			}
		}
		return v
	}
}

type histogram struct{}

func (histogram) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenHistogram, isNumber(col.Kind()))
}

func (histogram) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartBins, Bins: args.intOr("bins", 20), Unit: roundingOf(col, args).unit()}
}

func (histogram) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if len(p.Bins) == 0 {
		return nil, errors.New("profile has no bins")
	}
	g := newNumGen(col, apitypes.GenHistogram, p, args, s)
	w := make([]float64, len(p.Bins))
	for i, b := range p.Bins {
		w[i] = float64(b.Count)
	}
	cum := cumulative(w)
	g.draw = func() float64 {
		b := p.Bins[pickCum(cum, g.r)]
		return b.Lo + float64(g.r.Float64()*(b.Hi-b.Lo))
	}
	return g, nil
}

type bucket struct{}

func (bucket) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenBucket, isNumber(col.Kind()))
}

func (bucket) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartQuantiles, Unit: roundingOf(col, Args{}).unit()}
}

func (bucket) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if len(p.Quantiles) < 2 {
		return nil, errNoGrid
	}
	integer := col.Kind() == profile.KindInteger
	var edges []float64
	if list, ok := args["edges"].([]any); ok {
		for _, e := range list {
			edges = append(edges, e.(float64))
		}
	} else {
		// The outer edges would be near-exact source extremes, so only
		// the inner quantiles become edges.
		n := args.intOr("buckets", 5)
		for j := 1; j < n; j++ {
			e := roundSignificant(quantileAt(p.Quantiles, float64(j)/float64(n)), 2)
			if len(edges) == 0 || e > edges[len(edges)-1] {
				edges = append(edges, e)
			}
		}
	}
	labels := bucketLabels(edges, integer)
	round := roundingOf(col, Args{})
	lo, hi := clampToType(col, p.Lo, p.Hi)
	return &bucketGen{edges: edges, labels: labels, q: p.Quantiles, lo: lo, hi: hi, round: round, r: s.Values}, nil
}

type bucketGen struct {
	edges  []float64
	labels []string
	q      []float64
	lo, hi float64
	round  rounding
	r      *rng.Stream
}

func (g *bucketGen) Next(RowCtx) (any, error) {
	v := g.round.fit(gridValue(g.q, g.r.Float64(), true), g.lo, g.hi)
	// labels[0] is below edges[0], labels[i] is [edges[i-1], edges[i]).
	i := 0
	for i < len(g.edges) && v >= g.edges[i] {
		i++
	}
	return g.labels[i], nil
}

func (g *bucketGen) Pool() int64 { return int64(len(g.labels)) }

// bucketLabels returns len(edges)+1 labels: <e0, e0-e1, ..., >=em. On
// integer columns an inner label ends at the next edge minus 1 (30-39).
func bucketLabels(edges []float64, integer bool) []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	out := []string{"<" + f(edges[0])}
	for i := 0; i+1 < len(edges); i++ {
		a, b := edges[i], edges[i+1]
		if integer && a == math.Trunc(a) && b == math.Trunc(b) && b-1 >= a {
			out = append(out, f(a)+"-"+f(b-1))
		} else {
			out = append(out, f(a)+"-"+f(b))
		}
	}
	return append(out, ">="+f(edges[len(edges)-1]))
}

// quantileAt interpolates the grid q (at i/S) at probability prob.
func quantileAt(q []float64, prob float64) float64 {
	return gridValue(q, prob, true)
}

// roundSignificant rounds v to n significant digits.
func roundSignificant(v float64, n int) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	d := n - 1 - int(math.Floor(math.Log10(math.Abs(v))))
	m := math.Pow10(d)
	return math.Round(float64(v*m)) / m
}

type constant struct{}

func (constant) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenConstant, col.Kind() != profile.KindOther)
}

func (constant) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (constant) New(col Column, _ *profile.Column, args Args, _ Streams) (ColumnGen, error) {
	v, err := specValue(col, StageOf(col, apitypes.GenConstant), args["value"])
	if err != nil {
		return nil, errors.New("value: " + err.Error())
	}
	return fixed{v}, nil
}

// fixed always returns one value.
type fixed struct{ v any }

func (f fixed) Next(RowCtx) (any, error) { return f.v, nil }
func (fixed) Pool() int64                { return 1 }

type sequence struct{}

func (sequence) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenSequence, k == profile.KindInteger || k == profile.KindText)
}

func (sequence) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (sequence) New(col Column, _ *profile.Column, args Args, _ Streams) (ColumnGen, error) {
	return &seqGen{st: StageOf(col, apitypes.GenSequence), start: int64(args.intOr("start", 1)), step: int64(args.intOr("step", 1)), rows: col.Rows}, nil
}

type seqGen struct {
	st          Stage
	start, step int64
	rows        int64
}

func (g *seqGen) Next(row RowCtx) (any, error) {
	v := g.start + row.Index*g.step
	switch g.st {
	case StageVarchar:
		return strconv.FormatInt(v, 10), nil
	case StageDouble:
		return float64(v), nil
	}
	return v, nil
}

func (g *seqGen) Pool() int64 { return g.rows }

type rangeGen struct{}

func (rangeGen) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenRange, isNumber(col.Kind()))
}

func (rangeGen) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (rangeGen) New(col Column, _ *profile.Column, args Args, s Streams) (ColumnGen, error) {
	lo, _ := args.num("min")
	hi, _ := args.num("max")
	g := &numGen{st: StageOf(col, apitypes.GenRange), lo: lo, hi: hi, round: roundingOf(col, args), r: s.Values}
	g.draw = func() float64 { return lo + float64(g.r.Float64()*(hi-lo)) }
	return g, nil
}
