// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"math"
	"sort"
	"strconv"
	"time"
)

const (
	// maxAge caps PartAges, so its domain is fixed under noise.
	maxAge = 150
	// slots is the number of PartTimes slots in a day.
	slots = 86400 / TimeSlot
)

// ages reads PartAges after the number parts, which set hi' (concept
// 5.5). The reference is needs.AsOf or hi', never a source value, so the
// same data and spec give the same reference. A row after the reference
// counts as age 0, an age above maxAge as maxAge.
func (p *colRun) ages(needs Needs, s Settings, out *Column) error {
	ref := axisTime(p.kind, out.Hi)
	if needs.AsOf != nil {
		ref = needs.AsOf.UTC()
	}
	if p.kind == KindDate {
		ref = time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
	}
	out.AsOf = ref
	x := "CAST(" + p.x + " AS TIMESTAMP)"
	where := "isfinite(" + p.x + ")"
	bounded := p.noise != nil && s.Bounds != nil
	switch {
	case bounded:
		// Clamped like the number parts under bounds (plan 1.7).
		lo, hi, err := boundsAxis(p.kind, s.Bounds)
		if err != nil {
			return p.fail(err)
		}
		x = "least(greatest(" + x + ", " + tsLiteral(axisTime(p.kind, lo)) + "), " + tsLiteral(axisTime(p.kind, hi)) + ")"
	case s.Clip != nil:
		lo, hi, err := p.sourceRange(s)
		if err != nil {
			return err
		}
		where = p.axis + " BETWEEN " + floatLit(lo) + " AND " + floatLit(hi)
	}
	expr := "least(greatest(date_sub('year', " + x + ", " + tsLiteral(ref) + "), 0), " + strconv.Itoa(maxAge) + ")"
	if p.noise == nil {
		return p.nominal(&out.Ages, &out.Suppressed, "age", expr, where, 0)
	}
	if err := p.noisyFixed(&out.Ages, "ages", expr, where, 0, maxAge); err != nil {
		return err
	}
	if bounded && s.Clip != nil {
		// Age runs against the date axis of the clip quantiles.
		out.Ages = clipNominal(out.Ages, 1-s.Clip.Upper, 1-s.Clip.Lower)
		if len(out.Ages.Values) == 0 {
			return p.fail(ErrTooFewRows)
		}
	}
	return nil
}

// clipNominal cuts an int64 nominal part to its quantiles at lower and
// upper, as clipBins does for the number bins.
func clipNominal(n Nominal, lower, upper float64) Nominal {
	vs := append([]Value(nil), n.Values...)
	sort.Slice(vs, func(a, b int) bool { return vs[a].V.(int64) < vs[b].V.(int64) })
	bins := make([]Bin, len(vs))
	for i, v := range vs {
		f := float64(v.V.(int64))
		bins[i] = Bin{Lo: f, Hi: f, Count: v.Count}
	}
	var out []Value
	for _, b := range keptBins(clipBins(bins, lower, upper)) {
		out = append(out, Value{V: int64(b.Lo), Count: b.Count})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Count > out[b].Count })
	return Nominal{Values: out}
}

// timeSlot returns the PartTimes slot expression and its filter.
func (p *colRun) timeSlot() (expr, where string) {
	t, where := p.x, p.x+" IS NOT NULL"
	if p.kind == KindTimestamp {
		t, where = "CAST(CAST("+p.x+" AS TIMESTAMP) AS TIME)", "isfinite("+p.x+")"
	}
	// TIME '24:00:00' would be slot 96.
	return "least(CAST(floor(epoch(" + t + ") / " + strconv.Itoa(TimeSlot) + ") AS BIGINT), " + strconv.Itoa(slots-1) + ")", where
}

// axisTime converts a value on the number axis of a DATE or TIMESTAMP
// column to a time in UTC.
func axisTime(k Kind, v float64) time.Time {
	if k == KindDate {
		return time.Unix(int64(math.Floor(v))*86400, 0).UTC()
	}
	return time.UnixMicro(int64(math.Round(v * 1e6))).UTC()
}

// tsLiteral renders t as a TIMESTAMP literal. t comes from the spec or a
// jittered bound.
func tsLiteral(t time.Time) string {
	return "TIMESTAMP '" + t.Format("2006-01-02 15:04:05.999999") + "'"
}

func floatLit(v float64) string {
	return "CAST(" + strconv.FormatFloat(v, 'g', -1, 64) + " AS DOUBLE)"
}
