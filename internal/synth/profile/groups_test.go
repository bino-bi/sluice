// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/rng"
)

// groupFixture has two tables. src.crm.many has 121 groups of g with at
// least 5 rows: NULL (10 rows) and g000 to g119 (6 rows each), plus
// 'few' with 3 rows. src.crm.mixed has the group "it's" (30 rows, x
// from 0 to 28 and one row at 1000), 'flat' (8 rows, too few for a
// grid) and 'few' (2 rows). h is a second by column, true on every row.
const groupFixture = `
CREATE TABLE src.crm.many AS
SELECT CASE WHEN i < 10 THEN NULL WHEN i < 13 THEN 'few' ELSE 'g' || lpad(CAST((i - 13) // 6 AS VARCHAR), 3, '0') END AS g,
  CAST(i AS DOUBLE) AS x
FROM range(13 + 120 * 6) r(i);
CREATE TABLE src.crm.mixed AS
SELECT CASE WHEN i < 30 THEN 'it''s' WHEN i < 38 THEN 'flat' ELSE 'few' END AS g,
  true AS h,
  CAST(CASE WHEN i = 29 THEN 1000 WHEN i < 30 THEN i ELSE 5 END AS DOUBLE) AS x
FROM range(40) r(i);
`

func groupRunner(t *testing.T) *Runner {
	t.Helper()
	r, _ := newRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(), groupFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return r
}

func key(vals ...any) string {
	ns := make([]sql.NullString, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			ns[i] = sql.NullString{String: s, Valid: true}
		}
	}
	return GroupKey(ns)
}

func TestGroupKey(t *testing.T) {
	for _, tc := range []struct {
		vals []any
		want string
	}{
		{[]any{"a"}, "1:a"},
		{[]any{nil, "b:c"}, "n3:b:c"},
		{[]any{"", nil}, "0:n"},
	} {
		if got := key(tc.vals...); got != tc.want {
			t.Errorf("key %v = %q, want %q", tc.vals, got, tc.want)
		}
	}
	// A value that looks like an encoding gives another key.
	if key("1:a", "b") == key("1:a1:b") || key(nil) == key("n") {
		t.Error("keys collide")
	}
}

func TestGroupsCap(t *testing.T) {
	r := groupRunner(t)
	tb := Table{Catalog: "src", Schema: "crm", Name: "many"}
	col := ColumnInfo{Name: "x", Type: "DOUBLE"}
	s := Effective()
	var jitterKeys []string
	groups, stats, err := r.Groups(context.Background(), tb, col, []ColumnInfo{{Name: "g", Type: "VARCHAR"}},
		Needs{Parts: PartRange}, s, func(k string) *rng.Stream {
			jitterKeys = append(jitterKeys, k)
			return jitterStream(len(jitterKeys))
		})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Kept != MaxGroups || stats.Dropped != 21 || len(groups) != MaxGroups {
		t.Fatalf("stats = %+v, %d groups", stats, len(groups))
	}
	// Largest first, then by key with NULL first.
	if groups[0].Key != key(nil) || groups[1].Key != key("g000") || groups[99].Key != key("g098") {
		t.Errorf("keys %q %q ... %q", groups[0].Key, groups[1].Key, groups[99].Key)
	}
	if groups[0].Profile.InRange != 10 || groups[1].Profile.InRange != 6 {
		t.Errorf("rows in range: %d, %d", groups[0].Profile.InRange, groups[1].Profile.InRange)
	}
	if len(jitterKeys) != MaxGroups || jitterKeys[5] != groups[5].Key {
		t.Errorf("jitter called for %d keys", len(jitterKeys))
	}
	if n := scalar[int64](t, r, "SELECT count(*) FROM duckdb_tables() WHERE table_name = 'sluice_group'"); n != 0 {
		t.Error("group table left behind")
	}
}

func TestGroupsDropAndSpike(t *testing.T) {
	r := groupRunner(t)
	tb := Table{Catalog: "src", Schema: "crm", Name: "mixed"}
	col := ColumnInfo{Name: "x", Type: "DOUBLE"}
	s := Effective()
	s.JitterOff = true
	by := []ColumnInfo{{Name: "g", Type: "VARCHAR"}, {Name: "h", Type: "BOOLEAN"}}
	groups, stats, err := r.Groups(context.Background(), tb, col, by, Needs{Parts: PartQuantiles}, s,
		func(string) *rng.Stream { return jitterStream(0) })
	if err != nil {
		t.Fatal(err)
	}
	// flat fails the grid, few is below k.
	if stats != (GroupStats{Kept: 1, Dropped: 1, SpikeGroups: 1}) || len(groups) != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	g := groups[0]
	// The quote in the value is bound, not formatted into SQL.
	if g.Key != key("it's", "true") || g.Profile.InRange != 30 || g.Profile.Lo != 0 || g.Profile.Hi != 1000 {
		t.Errorf("group %q: in range %d, bounds %v %v", g.Key, g.Profile.InRange, g.Profile.Lo, g.Profile.Hi)
	}
	if len(g.Profile.Spikes) != 1 || g.Profile.Spikes[0].Side != SideHigh {
		t.Errorf("spikes = %+v", g.Profile.Spikes)
	}

	// A query error fails the call.
	_, _, err = r.Groups(context.Background(), tb, ColumnInfo{Name: "nope", Type: "DOUBLE"}, by, Needs{Parts: PartRange}, s,
		func(string) *rng.Stream { return jitterStream(0) })
	if !errors.Is(err, ErrQuery) {
		t.Errorf("err = %v, want ErrQuery", err)
	}
}
