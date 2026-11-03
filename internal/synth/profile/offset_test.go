// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"math"
	"testing"
)

// offsetFixture is src.crm.o, 200 rows. b is a plus 0 to 9 days, with an
// infinity row on each side; it is NULL for 13 rows, 3 of them where a is
// NULL too. t2 is t1 plus 5 or 6 seconds.
const offsetFixture = `
CREATE TABLE src.crm.o AS
SELECT
  i AS id,
  CASE WHEN i = 0 THEN DATE 'infinity' WHEN i % 25 = 4 THEN NULL ELSE DATE '2024-01-01' + CAST(i % 50 AS INTEGER) END AS a,
  CASE WHEN i = 1 THEN DATE 'infinity' WHEN i % 20 = 3 THEN NULL WHEN i % 25 = 4 AND i < 60 THEN NULL
       ELSE DATE '2024-01-01' + CAST(i % 50 + i % 10 AS INTEGER) END AS b,
  TIMESTAMP '2024-01-01 00:00:00' + i * INTERVAL 1 HOUR AS t1,
  TIMESTAMP '2024-01-01 00:00:05' + i * INTERVAL 1 HOUR + (i % 2) * INTERVAL 1 SECOND AS t2,
  'x' AS v
FROM range(200) r(i);
`

var offsetTbl = Table{Catalog: "src", Schema: "crm", Name: "o"}

// offsetProfile runs Column on one column of src.crm.o.
func offsetProfile(t *testing.T, r *Runner, name string, needs Needs, s Settings, seed int) (*Column, error) {
	t.Helper()
	ctx := context.Background()
	cols := []ColumnInfo{{Name: "a", Type: "DATE"}, {Name: "b", Type: "DATE"}, {Name: "t1", Type: "TIMESTAMP"},
		{Name: "t2", Type: "TIMESTAMP"}, {Name: "v", Type: "VARCHAR"}}
	counts, err := r.Base(ctx, offsetTbl, cols)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cols {
		if c.Name == name {
			return r.Column(ctx, offsetTbl, c, counts[i], needs, s, jitterStream(seed), nil)
		}
	}
	t.Fatalf("no column %s", name)
	return nil, nil
}

func offsetRunner(t *testing.T) *Runner {
	t.Helper()
	r, _ := newRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(), offsetFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return r
}

func TestOffsetAxis(t *testing.T) {
	r := offsetRunner(t)
	s := Effective()
	s.JitterOff = true

	// DATE: days, the infinity rows on either side drop out.
	needs := Needs{Parts: PartOffset | PartQuantiles, From: "a", KeepSign: true, Unit: 1}
	c, err := offsetProfile(t, r, "b", needs, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	finite := scalar[int64](t, r, "SELECT count(*) FROM src.crm.o WHERE isfinite(a) AND isfinite(b)")
	if c.Lo != 0 || c.Hi != 9 || c.InRange != finite {
		t.Errorf("date offset %v %v, in range %d, want 0 9 %d", c.Lo, c.Hi, c.InRange, finite)
	}
	// The null rate counts only rows where a is not NULL.
	want := scalar[float64](t, r, "SELECT count(*) FILTER (WHERE b IS NULL) / count(*) FROM src.crm.o WHERE a IS NOT NULL")
	plain := scalar[float64](t, r, "SELECT count(*) FILTER (WHERE b IS NULL) / count(*) FROM src.crm.o")
	if c.NullRate != want || want == plain {
		t.Errorf("null rate %v, want %v (plain %v)", c.NullRate, want, plain)
	}
	// A span offset reads no number part, but still the null rate.
	if c, err := offsetProfile(t, r, "b", Needs{Parts: PartOffset, From: "a"}, s, 0); err != nil || c.NullRate != want || c.Quantiles != nil {
		t.Errorf("span offset: %+v, %v", c, err)
	}

	// TIMESTAMP: seconds.
	needs = Needs{Parts: PartOffset | PartRange, From: "t1", KeepSign: true, Unit: 1}
	if c, err := offsetProfile(t, r, "t2", needs, s, 0); err != nil || c.Lo != 5 || c.Hi != 6 || c.NullRate != 0 {
		t.Errorf("timestamp offset: %+v, %v", c, err)
	}

	if _, err := offsetProfile(t, r, "v", Needs{Parts: PartOffset | PartRange, From: "a"}, s, 0); !errors.Is(err, ErrKind) {
		t.Errorf("offset on VARCHAR: err = %v, want ErrKind", err)
	}
	if _, err := offsetProfile(t, r, "b", Needs{Parts: PartOffset | PartRange}, s, 0); !errors.Is(err, ErrKind) {
		t.Errorf("offset without from: err = %v, want ErrKind", err)
	}
}

// KeepSign keeps lo' at 0 or above when the source offsets are, so a
// value is never drawn before its from value. Without it some jitter
// draws move lo' below 0.
func TestOffsetKeepSign(t *testing.T) {
	r := offsetRunner(t)
	s := Effective()
	s.JitterMin, s.JitterMax = 0.2, 0.2
	var below int
	for seed := range 40 {
		for _, keep := range []bool{true, false} {
			n := Needs{Parts: PartOffset | PartQuantiles, From: "a", KeepSign: keep, Unit: 1}
			c, err := offsetProfile(t, r, "b", n, s, seed)
			if err != nil {
				t.Fatal(err)
			}
			if keep && (c.Lo < 0 || c.Quantiles[0] < 0 || c.Hi < c.Lo) {
				t.Fatalf("seed %d: lo' %v, q0 %v, hi' %v", seed, c.Lo, c.Quantiles[0], c.Hi)
			}
			if !keep && c.Lo < 0 {
				below++
			}
		}
	}
	if below == 0 {
		t.Error("no seed moved lo' below 0 without KeepSign, the test shows nothing")
	}
}

// A timestamp offset range of 1 s moves by at least 1 s with Unit 1,
// so the bounds do not round back to the source bounds 5 and 6. With the
// timestamp default unit 0 the move is a few milliseconds.
func TestOffsetTimestampUnit(t *testing.T) {
	r := offsetRunner(t)
	back := 0
	for seed := range 50 {
		for _, unit := range []float64{1, 0} {
			n := Needs{Parts: PartOffset | PartRange, From: "t1", KeepSign: true, Unit: unit}
			c, err := offsetProfile(t, r, "t2", n, Effective(), seed)
			if err != nil {
				t.Fatal(err)
			}
			rounds := math.Round(c.Lo) == 5 || math.Round(c.Hi) == 6
			if unit == 1 && rounds {
				t.Fatalf("seed %d: bounds %v %v round to the source bounds", seed, c.Lo, c.Hi)
			}
			if unit == 0 && rounds {
				back++
			}
		}
	}
	if back == 0 {
		t.Error("unit 0 never rounds back, the test shows nothing")
	}
}
