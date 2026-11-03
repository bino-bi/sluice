// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// TestYearsTo pins yearsTo to values of DuckDB's date_sub('year').
func TestYearsTo(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     int
	}{
		{"2023-03-01 00:00:00", "2024-02-29 00:00:00", 0},
		{"2023-02-28 23:59:59", "2024-02-29 00:00:00", 1},
		{"2022-03-01 00:00:00", "2024-02-29 00:00:00", 1},
		{"2020-02-29 00:00:00", "2021-02-28 00:00:00", 1},
		{"2020-02-29 06:00:00", "2021-02-28 12:00:00", 1},
		{"2020-02-29 13:00:00", "2021-02-28 12:00:00", 0},
		{"2020-02-28 22:41:00", "2021-02-28 00:00:00", 0},
		{"2023-06-15 08:30:01", "2024-06-15 08:30:00", 0},
		{"2023-06-15 08:30:00", "2024-06-15 08:30:00", 1},
		{"1994-07-01 00:00:00", "2024-06-30 00:00:00", 29},
	} {
		f, _ := time.Parse(time.DateTime, c.from)
		r, _ := time.Parse(time.DateTime, c.to)
		if got := yearsTo(f, r); got != c.want {
			t.Errorf("yearsTo(%s, %s) = %d, want %d", c.from, c.to, got, c.want)
		}
	}
}

// Every written value keeps the drawn age, also after the cut to the
// granularity and with asOf at the end of February.
func TestAgeKeepsAge(t *testing.T) {
	for _, ref := range []time.Time{
		time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2021, 2, 28, 0, 0, 0, 0, time.UTC),
		time.Date(2023, 2, 28, 12, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 15, 8, 30, 0, 0, time.UTC),
	} {
		for _, c := range []struct{ typ, gran string }{
			{"DATE", "day"}, {"DATE", "month"}, {"DATE", "year"},
			{"TIMESTAMP", "second"}, {"TIMESTAMP", "hour"}, {"TIMESTAMP", "day"}, {"TIMESTAMP", "month"}, {"TIMESTAMP", "year"},
		} {
			if c.typ == "DATE" && ref.Hour() != 0 {
				continue
			}
			p := dateProfile()
			p.Ages = profile.Nominal{Values: []profile.Value{{V: int64(1), Count: 100}, {V: int64(30), Count: 100}}}
			p.AsOf = ref
			gc := genCase{gen: apitypes.GenAge, col: col("b", c.typ), args: Args{"granularity": c.gran}, p: p}
			for _, v := range draw(t, gc, 2000) {
				d := v.(time.Time)
				if a := yearsTo(d, ref); a != 1 && a != 30 {
					t.Fatalf("asOf %v, %s %s: %v has age %d", ref, c.typ, c.gran, d, a)
				}
				if !truncate(d, c.gran).Equal(d) {
					t.Fatalf("asOf %v, %s %s: %v not cut", ref, c.typ, c.gran, d)
				}
			}
		}
	}
}

// On a TIMESTAMP the added time of day never leaves the date range.
func TestTimeOfDayStaysInRange(t *testing.T) {
	p := tsProfile()
	// 2020-01-01 18:00 to 2020-01-03 08:00; slots at 03:00 and 21:00.
	p.Lo, p.Hi = 1577901600, 1578038400
	p.Quantiles = []float64{p.Lo, p.Hi}
	p.Times = profile.Nominal{Values: []profile.Value{{V: int64(12), Count: 100}, {V: int64(84), Count: 100}}}
	gc := genCase{gen: apitypes.GenTimeOfDay, col: col("t", "TIMESTAMP"), p: p}
	lo, hi := time.Unix(int64(p.Lo), 0), time.Unix(int64(p.Hi), 0)
	for _, v := range draw(t, gc, 2000) {
		d := v.(time.Time)
		if d.Before(lo) || d.After(hi) {
			t.Fatalf("%v outside [%v, %v]", d, lo.UTC(), hi.UTC())
		}
		if h := d.Hour(); h != 3 && h != 21 {
			t.Fatalf("%v: hour %d not in the source slots", d, h)
		}
	}
}
