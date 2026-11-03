// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"strconv"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func isDate(k profile.Kind) bool { return k == profile.KindDate || k == profile.KindTimestamp }

func granularity(col Column, args Args) string {
	def := "second"
	if col.Kind() == profile.KindDate {
		def = "day"
	}
	return args.strOr("granularity", def)
}

func checkDate(col Column, g apitypes.SyntheticGenerator, args Args) error {
	if err := fits(col, g, isDate(col.Kind())); err != nil {
		return err
	}
	if col.Kind() == profile.KindDate {
		switch granularity(col, args) {
		case "day", "month", "year":
		default:
			return errors.New("granularity on a DATE column must be day, month or year")
		}
	}
	return nil
}

// granUnit is the step of a granularity on the number axis: days for
// DATE, seconds otherwise. Month and year use their longest length.
func granUnit(col Column, gran string) float64 {
	days := map[string]float64{"day": 1, "month": 31, "year": 366}
	if col.Kind() == profile.KindDate {
		return days[gran]
	}
	switch gran {
	case "second":
		return 1
	case "minute":
		return 60
	case "hour":
		return 3600
	}
	return days[gran] * secondsPerDay
}

func truncate(t time.Time, gran string) time.Time {
	switch gran {
	case "second":
		return t.Truncate(time.Second)
	case "minute":
		return t.Truncate(time.Minute)
	case "hour":
		return t.Truncate(time.Hour)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

type dateDistribution struct{}

func (dateDistribution) Check(col Column, args Args) error {
	if err := checkDate(col, apitypes.GenDateDistribution, args); err != nil {
		return err
	}
	return checkBy(col, args)
}

func (dateDistribution) Needs(col Column, args Args) profile.Needs {
	n := profile.Needs{Parts: profile.PartQuantiles, Unit: granUnit(col, granularity(col, args))}
	if args.boolOr("keepWeekday", false) {
		n.Parts |= profile.PartWeekdays
	}
	if args.boolOr("keepSeasonality", false) {
		n.Parts |= profile.PartMonths
	}
	return n
}

func (dateDistribution) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if len(p.Quantiles) < 2 {
		return nil, errNoGrid
	}
	g := &dateGen{st: StageOf(col, apitypes.GenDateDistribution), lo: p.Lo, hi: p.Hi, gran: granularity(col, args), r: s.Values}
	g.draw = func() float64 { return gridValue(p.Quantiles, g.r.Float64(), true) }
	var err error
	if args.boolOr("keepSeasonality", false) {
		if g.months, err = newNominalPicker(p.Months); err != nil {
			return nil, err
		}
	}
	if args.boolOr("keepWeekday", false) {
		if g.weekdays, err = newNominalPicker(p.Weekdays); err != nil {
			return nil, err
		}
	}
	return g, nil
}

type dateRange struct{}

func (dateRange) Check(col Column, args Args) error {
	return checkDate(col, apitypes.GenDateRange, args)
}

func (dateRange) Needs(col Column, args Args) profile.Needs {
	_, okMin := args["min"]
	_, okMax := args["max"]
	if okMin && okMax {
		return profile.Needs{}
	}
	return profile.Needs{Parts: profile.PartRange, Unit: granUnit(col, granularity(col, args))}
}

var dateLayouts = []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339Nano}

func parseDate(v any) (time.Time, bool) {
	s, _ := v.(string)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (dateRange) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := &dateGen{st: StageOf(col, apitypes.GenDateRange), gran: granularity(col, args), r: s.Values}
	g.lo, g.hi = p.Lo, p.Hi
	if t, ok := parseDate(args["min"]); ok {
		g.lo = toAxis(g.st, t)
	}
	if t, ok := parseDate(args["max"]); ok {
		g.hi = toAxis(g.st, t)
	}
	if g.lo > g.hi {
		return nil, errors.New("min is after max")
	}
	g.draw = func() float64 { return g.lo + float64(g.r.Float64()*(g.hi-g.lo)) }
	return g, nil
}

// dateGen draws on the number axis, then truncates and moves the date
// (5.5).
type dateGen struct {
	st       Stage
	lo, hi   float64
	gran     string
	r        *rng.Stream
	draw     func() float64
	months   *nominalPicker
	weekdays *nominalPicker
}

func (g *dateGen) Pool() int64 { return -1 }

func (g *dateGen) in(t time.Time) bool {
	a := toAxis(g.st, t)
	return a >= g.lo && a <= g.hi
}

func (g *dateGen) next() time.Time {
	e := min(max(g.draw(), g.lo), g.hi)
	return truncate(fromAxis(g.st, e).(time.Time), g.gran)
}

// maxSeasonDraws caps the redraws of keepSeasonality.
const maxSeasonDraws = 16

func (g *dateGen) Next(RowCtx) (any, error) {
	t := g.next()
	if g.months != nil {
		for i := 0; ; i++ {
			c := withMonth(t, pickInt(g.months, g.r))
			if g.in(c) || i == maxSeasonDraws-1 {
				t = c
				break
			}
			t = g.next()
		}
	}
	if g.weekdays != nil {
		t = moveToWeekday(t, pickInt(g.weekdays, g.r), g.in)
	}
	return t, nil
}

func pickInt(np *nominalPicker, r *rng.Stream) int {
	n, _ := strconv.Atoi(np.next(r))
	return n
}

// withMonth keeps year, day and time and clamps the day to the month.
func withMonth(t time.Time, m int) time.Time {
	last := time.Date(t.Year(), time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(t.Year(), time.Month(m), min(t.Day(), last), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// moveToWeekday moves t to the nearest day with weekday w (0 is Sunday),
// at most 3 days, and the other way when that leaves the range.
func moveToWeekday(t time.Time, w int, in func(time.Time) bool) time.Time {
	d := (w - int(t.Weekday()) + 7) % 7
	if d > 3 {
		d -= 7
	}
	c := t.AddDate(0, 0, d)
	if !in(c) && d != 0 {
		if d > 0 {
			d -= 7
		} else {
			d += 7
		}
		c = t.AddDate(0, 0, d)
	}
	return c
}

type age struct{}

func (age) Check(col Column, args Args) error {
	return checkDate(col, apitypes.GenAge, args)
}

func (age) Needs(col Column, args Args) profile.Needs {
	n := profile.Needs{Parts: profile.PartRange | profile.PartAges, Unit: granUnit(col, granularity(col, args))}
	if t, ok := parseDate(args["asOf"]); ok {
		n.AsOf = &t
	}
	return n
}

func (age) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	ages, err := newNominalPicker(p.Ages)
	if err != nil {
		return nil, err
	}
	return &ageGen{asOf: p.AsOf, ages: ages, gran: granularity(col, args), r: s.Values}, nil
}

// ageGen draws an age, then a date in the year of life that has this age
// at asOf.
type ageGen struct {
	asOf time.Time
	ages *nominalPicker
	gran string
	r    *rng.Stream
}

func (g *ageGen) Pool() int64 { return -1 }

func (g *ageGen) Next(RowCtx) (any, error) {
	a := pickInt(g.ages, g.r)
	// The values in (lo, hi] have age a at asOf. Draw a start of a
	// granularity unit in it, so the cut value keeps age a.
	hi, lo := subYears(g.asOf, a), subYears(g.asOf, a+1)
	first := truncate(lo, g.gran)
	if !first.After(lo) {
		first = addUnit(first, g.gran, 1)
	}
	last := truncate(hi, g.gran)
	var n int
	switch g.gran {
	case "month":
		n = (last.Year()-first.Year())*12 + int(last.Month()-first.Month()) + 1
	case "year":
		n = last.Year() - first.Year() + 1
	default:
		n = int(last.Sub(first)/unitOf(g.gran)) + 1
	}
	// Around Feb 29 the values with age a are not one interval, so redraw
	// the few that miss.
	var t time.Time
	for i := 0; i < maxAgeDraws; i++ {
		if t = addUnit(first, g.gran, g.r.IntN(n)); yearsTo(t, g.asOf) == a {
			break
		}
	}
	return t, nil
}

// maxAgeDraws caps the redraws of age. A miss needs a value on Feb 28 or
// 29, so the cap is never reached in practice.
const maxAgeDraws = 16

// yearsTo counts whole years from t to ref as DuckDB's date_sub('year')
// does in the profile query.
func yearsTo(t, ref time.Time) int {
	if ref.AddDate(0, 0, 1).Day() == 1 && t.Month() == ref.Month() && t.Day() > ref.Day() {
		// DuckDB counts a later day as the last day of the month of ref.
		t = time.Date(t.Year(), t.Month(), ref.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	}
	a := ref.Year() - t.Year()
	tt := time.Date(ref.Year(), 1, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	rt := time.Date(ref.Year(), 1, 1, ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), time.UTC)
	if t.Month() > ref.Month() || t.Month() == ref.Month() && (t.Day() > ref.Day() || t.Day() == ref.Day() && tt.After(rt)) {
		a--
	}
	return a
}

// subYears is the latest value with n whole years to t, as DuckDB's
// date_sub('year') counts them. It counts all of Feb 28 as a full year
// to Feb 29, and Feb 29 like Feb 28 when t is the last day of February.
// time.AddDate would give Mar 1 for both.
func subYears(t time.Time, n int) time.Time {
	y := t.Year() - n
	last := time.Date(y, t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	d := t.Day()
	switch {
	case d > last:
		return time.Date(y, t.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Microsecond)
	case t.Month() == time.February && d == 28 && last == 29 && t.AddDate(0, 0, 1).Month() == time.March:
		d = 29
	}
	return time.Date(y, t.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

func unitOf(gran string) time.Duration {
	switch gran {
	case "minute":
		return time.Minute
	case "hour":
		return time.Hour
	case "day":
		return 24 * time.Hour
	}
	return time.Second
}

func addUnit(t time.Time, gran string, k int) time.Time {
	switch gran {
	case "month":
		return t.AddDate(0, k, 0)
	case "year":
		return t.AddDate(k, 0, 0)
	}
	return t.Add(time.Duration(k) * unitOf(gran))
}

type timeOfDay struct{}

func (timeOfDay) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenTimeOfDay, k == profile.KindTime || k == profile.KindTimestamp)
}

// Needs reads the dates of a TIMESTAMP as dateDistribution with
// granularity day does.
func (timeOfDay) Needs(col Column, _ Args) profile.Needs {
	if col.Kind() == profile.KindTimestamp {
		return profile.Needs{Parts: profile.PartQuantiles | profile.PartTimes, Unit: secondsPerDay}
	}
	return profile.Needs{Parts: profile.PartTimes}
}

func (timeOfDay) New(col Column, p *profile.Column, _ Args, s Streams) (ColumnGen, error) {
	slots, err := newNominalPicker(p.Times)
	if err != nil {
		return nil, err
	}
	g := &timeGen{slots: slots, r: s.Values}
	if col.Kind() == profile.KindTimestamp {
		if len(p.Quantiles) < 2 {
			return nil, errNoGrid
		}
		d := &dateGen{st: StageTimestamp, lo: p.Lo, hi: p.Hi, gran: "day", r: s.Values}
		d.draw = func() float64 { return gridValue(p.Quantiles, d.r.Float64(), true) }
		g.days = d
	}
	return g, nil
}

// timeGen draws a slot of the day and a second in it. With days it adds
// the second to a drawn day.
type timeGen struct {
	slots *nominalPicker
	days  *dateGen
	r     *rng.Stream
}

func (g *timeGen) Pool() int64 { return -1 }

func (g *timeGen) Next(RowCtx) (any, error) {
	sec := pickInt(g.slots, g.r)*profile.TimeSlot + g.r.IntN(profile.TimeSlot)
	if g.days == nil {
		return float64(sec), nil
	}
	// The day is cut to midnight, so the time can pass the range on its
	// first or last day. Move it one day into the range.
	t := g.days.next().Add(time.Duration(sec) * time.Second)
	lo, hi := fromAxis(StageTimestamp, g.days.lo).(time.Time), fromAxis(StageTimestamp, g.days.hi).(time.Time)
	if t.After(hi) {
		t = t.AddDate(0, 0, -1)
	}
	if t.Before(lo) {
		t = t.AddDate(0, 0, 1)
	}
	if t.After(hi) {
		t = hi
	}
	return t, nil
}
