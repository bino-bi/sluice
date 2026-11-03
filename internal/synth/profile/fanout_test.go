// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// fanOutFixture: parent src.crm.p has ids 1 to 30. Child src.crm.ch has
// 2 rows for ids 1 to 10, 3 rows for 11 to 20, 50 rows for id 30, none
// for 21 to 29, 6 orphan rows (999) and 4 NULL rows. src.crm.few has 2
// rows for ids 1 to 30 and 3 orphan rows.
const fanOutFixture = `
CREATE TABLE src.crm.p AS SELECT CAST(i AS BIGINT) AS id FROM range(1, 31) r(i);
CREATE TABLE src.crm.ch AS
SELECT CAST(fk AS BIGINT) AS fk FROM (
  SELECT i AS fk FROM range(1, 11) r(i), range(2) s(j)
  UNION ALL SELECT i FROM range(11, 21) r(i), range(3) s(j)
  UNION ALL SELECT 30 FROM range(50)
  UNION ALL SELECT 999 FROM range(6)
  UNION ALL SELECT NULL FROM range(4));
CREATE TABLE src.crm.few AS
SELECT CAST(fk AS BIGINT) AS fk FROM (
  SELECT i AS fk FROM range(1, 31) r(i), range(2) s(j)
  UNION ALL SELECT 999 FROM range(3));
`

func fanOutProfile(t *testing.T, r *Runner, table, col string, needs Needs) (*Column, error) {
	t.Helper()
	ctx := context.Background()
	tbl := Table{Catalog: "src", Schema: "crm", Name: table}
	info := ColumnInfo{Name: col, Type: "BIGINT"}
	counts, err := r.Base(ctx, tbl, []ColumnInfo{info})
	if err != nil {
		t.Fatal(err)
	}
	return r.Column(ctx, tbl, info, counts[0], needs, Effective(), jitterStream(0), nil)
}

func TestFanOut(t *testing.T) {
	r, _ := newRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(), fanOutFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	needs := Needs{Parts: PartFanOut, Parent: &Ref{Schema: "crm", Table: "p", Column: "id"}}
	p, err := fanOutProfile(t, r, "ch", "fk", needs)
	if err != nil {
		t.Fatal(err)
	}
	// The one parent with 50 rows moves to its neighbor, fan-out 3.
	want := []Bin{{Lo: 0, Hi: 0, Count: 9}, {Lo: 2, Hi: 2, Count: 10}, {Lo: 3, Hi: 3, Count: 11}}
	if !reflect.DeepEqual(p.FanOut, want) || p.MergedBins != 1 {
		t.Errorf("fan-out = %+v, merged %d", p.FanOut, p.MergedBins)
	}
	if want := 6.0 / 106; p.OrphanRate != want {
		t.Errorf("orphan rate = %v, want %v", p.OrphanRate, want)
	}

	// 3 orphans are below k: rate 0.
	p, err = fanOutProfile(t, r, "few", "fk", needs)
	if err != nil {
		t.Fatal(err)
	}
	if p.OrphanRate != 0 || !reflect.DeepEqual(p.FanOut, []Bin{{Lo: 2, Hi: 2, Count: 30}}) {
		t.Errorf("few: orphan rate %v, fan-out %+v", p.OrphanRate, p.FanOut)
	}

	if _, err := fanOutProfile(t, r, "ch", "fk", Needs{Parts: PartFanOut}); !errors.Is(err, ErrKind) {
		t.Errorf("no parent: err = %v, want ErrKind", err)
	}

	// Fewer than k parents.
	if _, err := r.Conn.ExecContext(context.Background(), "CREATE TABLE src.crm.p3 AS SELECT * FROM src.crm.p WHERE id <= 3"); err != nil {
		t.Fatal(err)
	}
	small := Needs{Parts: PartFanOut, Parent: &Ref{Schema: "crm", Table: "p3", Column: "id"}}
	if _, err := fanOutProfile(t, r, "ch", "fk", small); !errors.Is(err, ErrTooFewRows) {
		t.Errorf("3 parents: err = %v, want ErrTooFewRows", err)
	}
}
