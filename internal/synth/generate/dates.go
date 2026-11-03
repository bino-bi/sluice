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
