// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"time"

	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Errors of the noisy profile.
var (
	ErrNoiseUnsupported = errors.New("profile part is not supported with noise")
	ErrBounds           = errors.New("privacy.bounds does not fit the column type")
)

// Noise is the Laplace noise of one column (plan 1.4). A nil *Noise is
// off.
type Noise struct {
	// Epsilon is the budget of one measurement.
	Epsilon float64
	// Stream returns the stream of one measurement. The builder keys it
	// with the source fingerprint (plan 1.9).
	Stream func(measurement string) *rng.Stream
}

const (
	// numberCells is B_n of a number histogram without PartBins.
	numberCells = 200
	// maxOrdinal caps lengths and word counts, so their domain is fixed.
	maxOrdinal = 1024
	// classDelta: one row adds to up to 4 class counts.
	classDelta = 4
)

// noisyParts are the parts with a noisy variant. Measurements refuses
// every other part, so a new part cannot skip the noise.
const noisyParts = numberParts | PartTopValues | PartInitials | PartPatterns | PartPrefixes |
	PartLengths | PartWordCounts | PartWeekdays | PartMonths | PartCaseStyles | PartCharClasses | PartAges | PartTimes

// Measurements is the number of noisy measurements of needs (plan 1.5):
// one for all number parts except PartRange alone, one per other part.
// ok is false when needs reads a part without a noisy variant.
func Measurements(needs Needs) (n int, ok bool) {
	if needs.Parts&^noisyParts != 0 {
		return 0, false
	}
	n = bits.OnesCount32(uint32(needs.Parts &^ numberParts))
	if needs.Parts&(numberParts&^PartRange) != 0 {
		n++
	}
	return n, true
}

// cells draws one measurement (plan 4.3 steps 2 and 3): in cell order,
// round(c + Laplace(delta / epsilon)), at least 0. It returns the scale b.
func (n *Noise) cells(measurement string, c []int64, delta float64) ([]int64, float64) {
	r := n.Stream(measurement)
	b := delta / n.Epsilon
	out := make([]int64, len(c))
	for i, x := range c {
		out[i] = max(0, int64(math.Round(float64(x)+r.Laplace(b))))
	}
	return out, b
}

// threshold is tau of plan 1.6 for a fixed domain of n cells.
func threshold(b float64, n int, k int64) int64 {
	return max(k, int64(math.Ceil(float64(b*math.Log(float64(n))))))
}

// fixedCells draws a fixed-domain measurement and sets every cell below
// tau to 0.
func (n *Noise) fixedCells(measurement string, c []int64, delta float64, k int64) []int64 {
	out, b := n.cells(measurement, c, delta)
	tau := threshold(b, len(c), k)
	for i := range out {
		if out[i] < tau {
			out[i] = 0
		}
	}
	return out
}

// NoisyCounts applies the rows and nulls measurements (plan 4.3).
// noise("") is the table. Distinct is 0 (plan 1.5).
func NoisyCounts(c []Counts, cols []ColumnInfo, noise func(column string) *Noise) []Counts {
	if len(c) == 0 {
		return nil
	}
	rows, _ := noise("").cells("rows", []int64{c[0].Rows}, 1)
	out := make([]Counts, len(c))
	for i, x := range c {
		nulls, _ := noise(cols[i].Name).cells("nulls", []int64{x.Rows - x.NonNull}, 1)
		n := min(nulls[0], rows[0])
		out[i] = Counts{Rows: rows[0], NonNull: rows[0] - n}
	}
	return out
}

// NoisyRows is the noisy full row count of a sampled table, one more
// measurement of the table (plan 5, 1.5).
func NoisyRows(rows int64, noise func(column string) *Noise) int64 {
	out, _ := noise("").cells("sourceRows", []int64{rows}, 1)
	return out[0]
}

// valueKey orders nominal cells before the noise and breaks ties after
// it. It never leaves the package.
func valueKey(v any) string {
	switch x := v.(type) {
	case nil:
		return "n:"
	case string:
		return "s:" + x
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "i:" + fmt.Sprint(x)
	case float64:
		return "f:" + strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		return "f:" + strconv.FormatFloat(float64(x), 'g', -1, 32)
	case time.Time:
		return "t:" + x.UTC().Format(time.RFC3339Nano)
	case bool:
		return "b:" + strconv.FormatBool(x)
	}
	return fmt.Sprintf("%T:%v", v, v)
}

// noisyNominal applies plan 4.3 to a nominal part: the kept labels in
// valueKey order, then the rest cell. A label stays when its noisy count
// is at least k; a rest below k becomes 0.
func (p *colRun) noisyNominal(measurement string, values []Value, rest int64) ([]Value, int64) {
	keys := make([]string, len(values))
	idx := make([]int, len(values))
	for i, v := range values {
		keys[i], idx[i] = valueKey(v.V), i
	}
	sort.Slice(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	c := make([]int64, len(values)+1)
	for j, i := range idx {
		c[j] = values[i].Count
	}
	c[len(values)] = rest
	noisy, _ := p.noise.cells(measurement, c, 1)
	var out []Value
	var outKeys []string
	for j, i := range idx {
		if noisy[j] >= p.k {
			out = append(out, Value{V: values[i].V, Count: noisy[j]})
			outKeys = append(outKeys, keys[i])
		}
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := out[order[a]], out[order[b]]
		if x.Count != y.Count {
			return x.Count > y.Count
		}
		return outKeys[order[a]] < outKeys[order[b]]
	})
	sorted := make([]Value, len(out))
	for i, j := range order {
		sorted[i] = out[j]
	}
	rest = noisy[len(values)]
	if rest < p.k {
		rest = 0
	}
	return sorted, rest
}

// noisyNumbers is numbers under noise (plan 4.3, Numbers and Range only).
// The counts come from one histogram; quantiles, mean, standard deviation
// and log stats are read off it in Go.
func (p *colRun) noisyNumbers(needs Needs, s Settings, jitter *rng.Stream, out *Column) error {
	e := p.axis
	bounded := s.Bounds != nil
	var lo, hi float64
	var err error
	if bounded {
		if lo, hi, err = boundsAxis(p.kind, s.Bounds); err != nil {
			return p.fail(err)
		}
	} else if lo, hi, err = p.sourceRange(s); err != nil {
		return err
	}
	unit := max(needs.Unit, defaultUnit(p.typ))
	if needs.Parts&numberParts == PartRange {
		// The non-null count was checked on the noisy count.
		out.InRange = out.NonNull
		out.Lo, out.Hi = lo, hi
		if !bounded {
			out.Lo, out.Hi = p.jitter(lo, hi, unit, needs, s, jitter)
			return p.spikes(lo, hi, s, out)
		}
		return nil
	}

	n := numberCells
	if needs.Parts&PartBins != 0 {
		n = max(needs.Bins, 1)
	}
	if hi <= lo {
		n = 1
	}
	counts, err := p.binCounts(e, lo, hi, n, bounded)
	if err != nil {
		return err
	}
	noisy := p.noise.fixedCells("numbers", counts, 1, p.k)
	width := (hi - lo) / float64(n)
	bins := make([]Bin, n)
	for i := range bins {
		bins[i] = Bin{Lo: lo + float64(float64(i)*width), Hi: lo + float64(float64(i+1)*width), Count: noisy[i]}
	}
	bins[n-1].Hi = hi
	bins = keptBins(bins)
	if bounded && s.Clip != nil && len(bins) > 0 {
		bins = keptBins(clipBins(bins, s.Clip.Lower, s.Clip.Upper))
	}
	var total int64
	for _, b := range bins {
		total += b.Count
	}
	if total < p.k {
		return p.fail(ErrTooFewRows)
	}
	out.InRange = total

	var quantiles []float64
	if needs.Parts&PartQuantiles != 0 {
		steps := gridSteps(total, p.k)
		if steps < 2 {
			return p.fail(ErrGrid)
		}
		quantiles = make([]float64, steps+1)
		for i := range quantiles {
			quantiles[i] = binQuantile(bins, total, float64(i)/float64(steps))
		}
	}
	if needs.Parts&PartStats != 0 {
		out.Mean, out.StdDev = binMoments(bins, total, func(x float64) float64 { return x })
	}
	if needs.Parts&PartLogStats != 0 {
		for _, b := range bins {
			if !(b.Lo+b.Hi > 0) {
				return p.fail(ErrNotPositive)
			}
		}
		out.LogMean, out.LogStdDev = binMoments(bins, total, math.Log)
	}

	effLo, effHi := bins[0].Lo, bins[len(bins)-1].Hi
	out.axisQ = append([]float64(nil), quantiles...)
	if bounded {
		out.Lo, out.Hi = effLo, effHi
	} else {
		jlo, jhi := p.jitter(lo, hi, unit, needs, s, jitter)
		out.Lo, out.Hi = mapLinear(effLo, lo, hi, jlo, jhi), mapLinear(effHi, lo, hi, jlo, jhi)
		for i, q := range quantiles {
			quantiles[i] = mapLinear(q, lo, hi, jlo, jhi)
		}
		for i := range bins {
			bins[i].Lo = mapLinear(bins[i].Lo, lo, hi, jlo, jhi)
			bins[i].Hi = mapLinear(bins[i].Hi, lo, hi, jlo, jhi)
		}
	}
	out.Quantiles = quantiles
	if needs.Parts&PartBins != 0 {
		out.Bins = bins
	}
	// With bounds the range is public and tau hides a lone extreme. Without
	// them the spike check reads exact counts (not covered, plan 1.8).
	if !bounded {
		return p.spikes(lo, hi, s, out)
	}
	return nil
}

// binCounts reads an equal-width histogram of n bins on [lo, hi]. With
// clamp the values are first clamped into [lo, hi] (plan 1.7); without it
// only values inside count. Every bin is returned, also empty ones.
func (p *colRun) binCounts(e string, lo, hi float64, n int, clamp bool) ([]int64, error) {
	counts := make([]int64, n)
	var q string
	var args []any
	width := (hi - lo) / float64(n)
	switch {
	case clamp:
		q = "SELECT least(greatest(CAST(floor((least(greatest(" + e + ", ?), ?) - ?) / ?) AS BIGINT), 0), ?) AS b, count(*) FROM " +
			p.from + " WHERE isfinite(" + e + ") GROUP BY b"
		args = []any{lo, hi, lo, width, int64(n - 1)}
	case width == 0:
		q = "SELECT 0 AS b, count(*) FROM " + p.from + " WHERE " + e + " BETWEEN ? AND ?"
		args = []any{lo, hi}
	default:
		q = "SELECT least(CAST(floor((" + e + " - ?) / ?) AS BIGINT), ?) AS b, count(*) FROM " + p.from +
			" WHERE " + e + " BETWEEN ? AND ? GROUP BY b"
		args = []any{lo, width, int64(n - 1), lo, hi}
	}
	err := p.query(q, args, func(rows *sql.Rows) error {
		var b, c int64
		if err := rows.Scan(&b, &c); err != nil {
			return err
		}
		if b < 0 || b >= int64(n) {
			return fmt.Errorf("bin %d out of range", b)
		}
		counts[b] = c
		return nil
	})
	return counts, err
}

// keptBins drops the bins with count 0.
func keptBins(bins []Bin) []Bin {
	var out []Bin
	for _, b := range bins {
		if b.Count > 0 {
			out = append(out, b)
		}
	}
	return out
}

// binQuantile is the quantile at prob of the histogram bins (total rows),
// linear inside a bin.
func binQuantile(bins []Bin, total int64, prob float64) float64 {
	target := float64(prob * float64(total))
	var cum float64
	for _, b := range bins {
		c := float64(b.Count)
		if cum+c >= target {
			return b.Lo + float64((target-cum)/c*(b.Hi-b.Lo))
		}
		cum += c
	}
	return bins[len(bins)-1].Hi
}

// clipBins cuts the bins to the quantiles at lower and upper. A partial
// bin keeps its share of the count.
func clipBins(bins []Bin, lower, upper float64) []Bin {
	var total int64
	for _, b := range bins {
		total += b.Count
	}
	lo, hi := binQuantile(bins, total, lower), binQuantile(bins, total, upper)
	var out []Bin
	for _, b := range bins {
		a, z := max(b.Lo, lo), min(b.Hi, hi)
		if z < a || z == a && b.Hi > b.Lo {
			continue
		}
		c := b.Count
		if b.Hi > b.Lo {
			c = int64(math.Round(float64(float64(c) * (z - a) / (b.Hi - b.Lo))))
		}
		out = append(out, Bin{Lo: a, Hi: z, Count: c})
	}
	return out
}

// binMoments returns mean and sample standard deviation of f(midpoint),
// weighted by the bin counts.
func binMoments(bins []Bin, total int64, f func(float64) float64) (float64, float64) {
	n := float64(total)
	var sum float64
	for _, b := range bins {
		sum += float64(float64(b.Count) * f(float64(0.5*(b.Lo+b.Hi))))
	}
	mean := sum / n
	if total < 2 {
		return mean, 0
	}
	var ss float64
	for _, b := range bins {
		d := f(float64(0.5*(b.Lo+b.Hi))) - mean
		ss += float64(float64(b.Count) * float64(d*d))
	}
	return mean, math.Sqrt(ss / (n - 1))
}

// noisyFixed reads a fixed-domain nominal part with one cell per value
// lo..hi of expr (weekdays, months). The query has no HAVING: a cell
// zeroed below k before the noise would make the sensitivity k.
func (p *colRun) noisyFixed(dst *Nominal, measurement, expr, where string, lo, hi int64) error {
	c, err := p.cellCounts(expr, where, lo, hi)
	if err != nil {
		return err
	}
	noisy := p.noise.fixedCells(measurement, c, 1, p.k)
	var values []Value
	for i, n := range noisy {
		if n > 0 {
			values = append(values, Value{V: lo + int64(i), Count: n})
		}
	}
	if len(values) == 0 {
		return p.fail(fmt.Errorf("%w (%s)", ErrNoGroups, measurement))
	}
	sort.SliceStable(values, func(a, b int) bool { return values[a].Count > values[b].Count })
	*dst = Nominal{Values: values}
	return nil
}

// cellCounts reads the exact count of every value lo..hi of expr, with
// no k filter.
func (p *colRun) cellCounts(expr, where string, lo, hi int64) ([]int64, error) {
	c := make([]int64, hi-lo+1)
	q := "SELECT " + expr + " AS v, count(*) FROM " + p.from + " WHERE " + where + " GROUP BY v"
	err := p.query(q, nil, func(rows *sql.Rows) error {
		var v, n int64
		if err := rows.Scan(&v, &n); err != nil {
			return err
		}
		if v < lo || v > hi {
			return fmt.Errorf("cell %d out of range", v)
		}
		c[v-lo] = n
		return nil
	})
	return c, err
}

// noisyOrdinal reads an ordinal part with one cell per value from lo to
// maxOrdinal; larger values count in the last cell.
func (p *colRun) noisyOrdinal(dst *[]Bin, measurement, expr, where string, lo int64) error {
	c := make([]int64, maxOrdinal-lo+1)
	q := "SELECT greatest(least(" + expr + ", " + strconv.Itoa(maxOrdinal) + "), " + strconv.FormatInt(lo, 10) +
		") AS l, count(*) FROM " + p.from + " WHERE " + where + " GROUP BY l"
	err := p.query(q, nil, func(rows *sql.Rows) error {
		var l, n int64
		if err := rows.Scan(&l, &n); err != nil {
			return err
		}
		c[l-lo] = n
		return nil
	})
	if err != nil {
		return err
	}
	noisy := p.noise.fixedCells(measurement, c, 1, p.k)
	var bins []Bin
	var total int64
	for i, n := range noisy {
		if n > 0 {
			v := float64(lo + int64(i))
			bins = append(bins, Bin{Lo: v, Hi: v, Count: n})
			total += n
		}
	}
	if total < p.k {
		return p.fail(ErrTooFewRows)
	}
	*dst = bins
	return nil
}

// boundsAxis returns privacy.bounds on the number axis of kind k (plan
// 1.7): numbers for number kinds and TIME, ISO dates or timestamps in UTC
// for DATE (days, floor) and TIMESTAMP (seconds).
func boundsAxis(k Kind, b *apitypes.SyntheticBounds) (float64, float64, error) {
	lo, okLo := axisValue(k, b.Min)
	hi, okHi := axisValue(k, b.Max)
	if !okLo || !okHi || !(lo < hi) {
		return 0, 0, ErrBounds
	}
	return lo, hi, nil
}

var dateLayouts = []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339Nano}

func axisValue(k Kind, v any) (float64, bool) {
	switch k {
	case KindInteger, KindFloat, KindTime:
		f, ok := v.(float64)
		return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
	case KindDate, KindTimestamp:
		s, _ := v.(string)
		for _, l := range dateLayouts {
			t, err := time.Parse(l, s)
			if err != nil {
				continue
			}
			if k == KindDate {
				return math.Floor(float64(t.Unix()) / 86400), true
			}
			return float64(t.UnixMicro()) / 1e6, true
		}
	}
	return 0, false
}
