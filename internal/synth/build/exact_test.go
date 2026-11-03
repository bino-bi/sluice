// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// exactSpec lists cols in the customers privacy. The names are fake
// without keepInitial, so a synthetic pair equals one of the 3 source
// list pairs with probability 3/676 (plan section 9). email reads both
// names, so a redraw must draw it again.
func exactSpec(list ...string) *apitypes.SyntheticSpec {
	spec := relSpec(map[string]apitypes.SyntheticColumn{
		"first_name": {Generator: apitypes.GenFake, Args: map[string]any{"kind": "first_name"}},
		"last_name":  {Generator: apitypes.GenFake, Args: map[string]any{"kind": "last_name"}},
		"email":      {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{first_name}.{last_name}@example.org", "lower": true}},
	})
	spec.Tables[0].Privacy = &apitypes.SyntheticPrivacy{RejectExactMatches: list}
	return spec
}

// leakMarkers are fixture values no error or report may hold.
var leakMarkers = []string{"Hamburg", "Bremen", "Kiel", "Essen", "vip", "active", "paused", "closed", "Alex", "Adams", "Blair", "Casey"}

// R11: too many matches fail with counts and names only.
func TestRejectExactMatchesTooMany(t *testing.T) {
	spec := exactSpec("city", "status")
	spec.Tables[0].Columns["city"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	spec.Tables[0].Columns["status"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "table crm.customers: ") ||
		!strings.HasSuffix(msg, " of 1000 rows equal a source row on city, status (more than 1 %), the columns are too predictable together") {
		t.Errorf("error = %q", msg)
	}
	for _, m := range leakMarkers {
		if strings.Contains(msg, m) {
			t.Errorf("error %q holds %q", msg, m)
		}
	}
}

// R11: a few matches are drawn again; the snapshot has none left.
func TestRejectExactMatchesRedraw(t *testing.T) {
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: exactSpec("First_Name", "last_name"), dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	em := rep.Tables[0].ExactMatches
	if em == nil {
		t.Fatal("no exact_matches in the report")
	}
	if !slices.Equal(em.Columns, []string{"first_name", "last_name"}) || em.MatchedRows == 0 || em.Rounds == 0 || em.RedrawnRows < em.MatchedRows {
		t.Errorf("exact_matches = %+v", em)
	}
	// The seed is fixed, so the counts are too (about 4 expected).
	if runtime.GOARCH == "amd64" && (em.MatchedRows != 1 || em.Rounds != 1 || em.RedrawnRows != 1) {
		t.Errorf("exact_matches = %+v, want 1 row matched and redrawn in 1 round", em)
	}
	for _, q := range []string{
		`SELECT count(*) FROM syn.crm.customers s WHERE EXISTS (SELECT 1 FROM src.crm.customers t
		   WHERE s.first_name IS NOT DISTINCT FROM t.first_name AND s.last_name IS NOT DISTINCT FROM t.last_name)`,
		"SELECT count(*) FROM syn.crm.customers WHERE email IS DISTINCT FROM lower(first_name || '.' || last_name || '@example.org')",
	} {
		if n := scalar[int64](t, db, q); n != 0 {
			t.Errorf("%d rows: %s", n, q)
		}
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers"); n != 1000 {
		t.Errorf("%d rows, want 1000", n)
	}

	// The redraw is part of the content hash and fixed by the seed.
	again, _ := mustRun(t, opts{spec: exactSpec("First_Name", "last_name"), source: relationsPath(t)})
	if again.ContentHash != rep.ContentHash {
		t.Errorf("content hash %s, then %s", rep.ContentHash, again.ContentHash)
	}
	// A list without matches and no list at all give the same rows.
	plain, _ := mustRun(t, opts{spec: exactSpec("email"), source: relationsPath(t)})
	none := exactSpec()
	none.Tables[0].Privacy = nil
	unlisted, _ := mustRun(t, opts{spec: none, source: relationsPath(t)})
	if plain.ContentHash == rep.ContentHash || plain.ContentHash != unlisted.ContentHash || plain.Tables[0].ExactMatches.Rounds != 0 {
		t.Errorf("without redraws: hash %s, no list %s, exact_matches %+v", plain.ContentHash, unlisted.ContentHash, plain.Tables[0].ExactMatches)
	}
}

func TestRejectExactMatchesUnknownColumn(t *testing.T) {
	_, err := run(t, opts{spec: exactSpec("first_name", "nope"), source: relationsPath(t)})
	want := "table crm.customers: privacy.rejectExactMatches names column nope, which is not in the table"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// R11: the round loop with a fake matcher.
func TestRedrawRounds(t *testing.T) {
	names := []string{"a", "b"}
	noRedraw := func([]int64, int) error { return nil }
	t.Run("one percent", func(t *testing.T) {
		first := make([]int64, 11)
		_, err := redrawRounds(names, first, 1000, nil, noRedraw)
		want := "11 of 1000 rows equal a source row on a, b (more than 1 %), the columns are too predictable together"
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
		if _, err := redrawRounds(names, first[:10], 1000, func(only []int64) ([]int64, error) { return nil, nil }, noRedraw); err != nil {
			t.Fatalf("10 of 1000: %v", err)
		}
	})
	t.Run("still matching", func(t *testing.T) {
		var calls int
		_, err := redrawRounds(names, []int64{3, 7, 9}, 1000, func(only []int64) ([]int64, error) {
			return only, nil
		}, func(_ []int64, round int) error {
			calls++
			if round != calls {
				t.Errorf("round %d on call %d", round, calls)
			}
			return nil
		})
		want := "3 rows still equal a source row after 10 redraws"
		if err == nil || err.Error() != want || calls != maxRedrawRounds {
			t.Fatalf("err = %v after %d redraws, want %q", err, calls, want)
		}
	})
	t.Run("clears", func(t *testing.T) {
		var redrawn [][]int64
		rep, err := redrawRounds(names, []int64{3, 7, 9}, 1000, func(only []int64) ([]int64, error) {
			return only[1:], nil
		}, func(rns []int64, _ int) error {
			redrawn = append(redrawn, rns)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if rep.MatchedRows != 3 || rep.RedrawnRows != 6 || rep.Rounds != 3 || len(redrawn) != 3 || !slices.Equal(redrawn[2], []int64{9}) {
			t.Errorf("report %+v, redrawn %v", rep, redrawn)
		}
	})
	t.Run("errors", func(t *testing.T) {
		boom := errors.New("boom")
		if _, err := redrawRounds(names, []int64{1}, 1000, nil, func([]int64, int) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("redraw error: %v", err)
		}
		if _, err := redrawRounds(names, []int64{1}, 1000, func([]int64) ([]int64, error) { return nil, boom }, noRedraw); !errors.Is(err, boom) {
			t.Errorf("match error: %v", err)
		}
	})
}

// The listed columns and everything that reads them, in evaluation order.
func TestRedrawSet(t *testing.T) {
	cols := []*column{{}, {refs: []string{"c"}}, {}, {refs: []string{"b"}}, {}}
	for i, n := range []string{"a", "B", "c", "d", "e"} {
		cols[i].info.Name = n
	}
	// d reads b, b reads c; a and e are free.
	got := redrawSet(cols, []int{0, 2, 1, 3, 4}, []int{2, 4})
	if want := []int{2, 1, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("redrawSet = %v, want %v", got, want)
	}
}

// A redrawn parent key column hands the final keys to its children, in
// row order and without NULLs.
func TestRedrawReloadsKeys(t *testing.T) {
	ctx := context.Background()
	conn := memConn(t)

	// The key column reads the value column, so a redraw of v redraws k.
	var draws int64
	cols := []*column{
		{stage: generate.StageBigint, gen: stageFunc(func(r generate.RowCtx) (any, error) {
			draws++
			if r.Index == 2 {
				return nil, nil
			}
			return 100*draws + r.Index, nil
		})},
		{stage: generate.StageBigint, refs: []string{"v"}, keyOut: &generate.KeySet{}, gen: stageFunc(func(r generate.RowCtx) (any, error) {
			if r.Values[0] == nil {
				return nil, nil
			}
			return r.Values[0].(int64) + 1, nil
		})},
	}
	cols[0].info.Name, cols[1].info.Name = "v", "k"
	b := &builder{hash: sha256.New(), log: slog.New(slog.DiscardHandler)}
	if err := b.stage(ctx, conn, cols, []int{0, 1}, 4, true); err != nil {
		t.Fatal(err)
	}
	set := redrawSet(cols, []int{0, 1}, []int{0})
	if err := b.redraw(ctx, conn, cols, set, []int64{1}, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.reloadKeys(ctx, conn, cols[1], 1); err != nil {
		t.Fatal(err)
	}
	// Main pass: values 100, 201, NULL, 403. The redraw of row 1 is draw 5.
	want := []any{int64(101), int64(502), int64(404)}
	if fmt.Sprint(cols[1].keyOut.Values) != fmt.Sprint(want) {
		t.Errorf("keys = %v, want %v", cols[1].keyOut.Values, want)
	}
}

func memConn(t *testing.T) *sql.Conn {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// rejectExact with a parent key column that reads the listed column v and
// a foreignKey column f. The source holds one row equal to staged row 1.
func TestRejectExactKeys(t *testing.T) {
	const rows = 100
	setup := func(t *testing.T) (*builder, *sql.Conn, []*column) {
		ctx := context.Background()
		conn := memConn(t)
		if _, err := conn.ExecContext(ctx, "CREATE TABLE src AS SELECT 10::BIGINT AS v, 11::BIGINT AS k, 51::BIGINT AS f"); err != nil {
			t.Fatal(err)
		}
		seen := map[int64]bool{}
		fkDrawn := map[int64]bool{}
		cols := []*column{
			{gen: stageFunc(func(r generate.RowCtx) (any, error) {
				if seen[r.Index] {
					return 900 + r.Index, nil
				}
				seen[r.Index] = true
				return 10 * r.Index, nil
			})},
			{refs: []string{"v"}, keyOut: &generate.KeySet{}, gen: stageFunc(func(r generate.RowCtx) (any, error) {
				return r.Values[0].(int64) + 1, nil
			})},
			{choice: generate.Choice{Generator: apitypes.GenForeignKey, Args: map[string]any{"table": "crm.customers", "column": "id"}},
				gen: stageFunc(func(r generate.RowCtx) (any, error) {
					if fkDrawn[r.Index] {
						t.Errorf("foreignKey drawn again for row %d", r.Index)
					}
					fkDrawn[r.Index] = true
					return 50 + r.Index, nil
				})},
		}
		for i, n := range []string{"v", "k", "f"} {
			cols[i].info.Name, cols[i].stage, cols[i].snapType = n, generate.StageBigint, "BIGINT"
		}
		b := &builder{hash: sha256.New(), log: slog.New(slog.DiscardHandler)}
		if err := b.stage(ctx, conn, cols, []int{0, 1, 2}, rows, true); err != nil {
			t.Fatal(err)
		}
		return b, conn, cols
	}
	pt := profile.Table{Catalog: "memory", Schema: "main", Name: "src"}
	staged := func(t *testing.T, conn *sql.Conn, j int) []int64 {
		r, err := conn.QueryContext(context.Background(), "SELECT "+stageName(j)+" FROM "+stageTable+" ORDER BY "+rnColumn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Close() }()
		var out []int64
		for r.Next() {
			var v int64
			if err := r.Scan(&v); err != nil {
				t.Fatal(err)
			}
			out = append(out, v)
		}
		return out
	}

	t.Run("parent keys", func(t *testing.T) {
		b, conn, cols := setup(t)
		rep, err := b.rejectExact(context.Background(), conn, pt, cols, []int{0, 1, 2}, []int{0}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if rep.MatchedRows != 1 || rep.Rounds != 1 {
			t.Fatalf("report %+v", rep)
		}
		keys := staged(t, conn, 1)
		if keys[1] != 902 {
			t.Fatalf("staged key of row 1 = %d, want 902", keys[1])
		}
		if fmt.Sprint(cols[1].keyOut.Values) != fmt.Sprint(keys) {
			t.Errorf("keys handed to children = %v, staged %v", cols[1].keyOut.Values, keys)
		}
	})
	t.Run("foreignKey listed", func(t *testing.T) {
		b, conn, cols := setup(t)
		before := staged(t, conn, 2)
		rep, err := b.rejectExact(context.Background(), conn, pt, cols, []int{0, 1, 2}, []int{0, 2}, rows)
		if err != nil {
			t.Fatal(err)
		}
		if rep.MatchedRows != 1 || rep.Rounds != 1 {
			t.Fatalf("report %+v", rep)
		}
		if after := staged(t, conn, 2); !slices.Equal(before, after) {
			t.Errorf("foreignKey values changed: %v, then %v", before, after)
		}
		if v := staged(t, conn, 0); v[1] != 901 {
			t.Errorf("v of row 1 = %d, want 901", v[1])
		}
	})
}

// A failed match query that is not a cast failure names the step.
func TestExactMatchesQueryError(t *testing.T) {
	ctx := context.Background()
	conn := memConn(t)
	cols := []*column{{gen: stageFunc(func(r generate.RowCtx) (any, error) { return r.Index, nil })}}
	cols[0].info.Name, cols[0].stage, cols[0].snapType = "v", generate.StageBigint, "BIGINT"
	b := &builder{hash: sha256.New(), log: slog.New(slog.DiscardHandler)}
	if err := b.stage(ctx, conn, cols, []int{0}, 3, true); err != nil {
		t.Fatal(err)
	}
	_, err := b.exactMatches(ctx, conn, profile.Table{Catalog: "memory", Schema: "main", Name: "missing"}, cols, []int{0}, nil)
	if err == nil || err.Error() != "cannot check exact matches" {
		t.Errorf("err = %v, want cannot check exact matches", err)
	}
}
