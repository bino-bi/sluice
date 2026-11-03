// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"math"

	"github.com/bino-bi/sluice/internal/synth/rng"
)

// maxGridSteps caps the quantile grid.
const maxGridSteps = 100

// nullRate applies 4.2 step 2.
func nullRate(c Counts, k int64) float64 {
	nulls := c.Rows - c.NonNull
	switch {
	case nulls < k:
		return 0
	case c.NonNull < k:
		return 1
	}
	return float64(nulls) / float64(c.Rows)
}

// gridSteps returns S of 4.2 step 4: every step holds at least k rows.
func gridSteps(n, k int64) int {
	return int(min(maxGridSteps, n/k))
}

// mergeOrdinal applies 4.2 step 6. A bin with fewer than k rows moves
// its rows to the neighbor with more rows (the lower one on a tie). The
// neighbor keeps its own value or range, so the rare value never shows.
// The smallest bin is merged first (the lowest one on a tie). It
// returns the bins left and the number of merges. Bins with Rows set
// are merged on Rows, so the unit is the source row; merged Rows are
// summed, which can overcount when a row sits in several bins.
func mergeOrdinal(bins []Bin, k int64) ([]Bin, int) {
	out, _, merges := mergeSpans(bins, k)
	return out, merges
}

// mergeSpans is mergeOrdinal that also returns, for every bin left, the
// index range [first, last] of the input bins merged into it.
func mergeSpans(bins []Bin, k int64) ([]Bin, [][2]int, int) {
	out := append([]Bin(nil), bins...)
	spans := make([][2]int, len(out))
	for i := range spans {
		spans[i] = [2]int{i, i}
	}
	weight := func(b Bin) int64 {
		if b.Rows > 0 {
			return b.Rows
		}
		return b.Count
	}
	merges := 0
	for len(out) > 1 {
		i := -1
		for j, b := range out {
			if weight(b) < k && (i < 0 || weight(b) < weight(out[i])) {
				i = j
			}
		}
		if i < 0 {
			break
		}
		to := i - 1
		if i == 0 || (i+1 < len(out) && weight(out[i+1]) > weight(out[i-1])) {
			to = i + 1
		}
		if out[i].Rows > 0 || out[to].Rows > 0 {
			out[to].Rows = weight(out[to]) + weight(out[i])
		}
		out[to].Count += out[i].Count
		spans[to] = [2]int{min(spans[to][0], spans[i][0]), max(spans[to][1], spans[i][1])}
		out = append(out[:i], out[i+1:]...)
		spans = append(spans[:i], spans[i+1:]...)
		merges++
	}
	return out, spans, merges
}

// jitterBounds applies 4.2 step 7 and returns lo' and hi'. The draws
// come from r in a fixed order: share and sign of lo, then of hi.
func jitterBounds(lo, hi, unit float64, s Settings, r *rng.Stream) (float64, float64) {
	width := hi - lo
	if s.JitterOff || width <= 0 {
		return lo, hi
	}
	draw := func() (float64, float64) {
		share := s.JitterMin + float64(r.Float64()*(s.JitterMax-s.JitterMin))
		move := float64(share * width)
		if move < unit {
			move = unit
		}
		sign := 1.0
		if r.Float64() < 0.5 {
			sign = -1
		}
		return move, sign
	}
	ma, sa := draw()
	mb, sb := draw()
	lo2 := lo + float64(sa*ma)
	hi2 := hi + float64(sb*mb)
	if lo2 >= hi2 {
		// Flip a sign that shrank the range. A move is at most half the
		// width or one unit, so one flip is enough.
		if sa > 0 {
			lo2 = lo - ma
		} else {
			hi2 = hi + mb
		}
	}
	return lo2, hi2
}

// mapLinear maps v from [lo, hi] onto [lo2, hi2].
func mapLinear(v, lo, hi, lo2, hi2 float64) float64 {
	if hi <= lo {
		return v
	}
	return lo2 + float64((v-lo)*(hi2-lo2))/(hi-lo)
}

// spikesOf applies 4.2 step 8 to the row counts at the low and high end.
func spikesOf(low, high, k int64) []Spike {
	var out []Spike
	if low > 0 && low < k {
		out = append(out, Spike{Side: SideLow, Rows: low})
	}
	if high > 0 && high < k {
		out = append(out, Spike{Side: SideHigh, Rows: high})
	}
	return out
}

// finite reports whether every value is a finite number.
func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
