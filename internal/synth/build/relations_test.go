// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// relationsSQL is the relations fixture (plan section 9), fixed formulas
// only. p is a permutation of 0 to 4999, so status has exact counts:
// vip 3, closed 497, paused 1500, active 3000. Names are not in the fake
// lists, except the pairs of ids 11, 12 and 13. email is NULL for 10 %.
// orders has 15 400 rows: customer i has i % 7 orders, customer 42 has
// 400, and 3 orders have a customer that does not exist. shipped_at is
// ordered_at plus 1 to 10 days, NULL for 5 %.
const relationsSQL = `
CREATE SCHEMA crm;
CREATE TABLE crm.customers AS
WITH b AS (SELECT i, (i * 7919) % 5000 AS p FROM range(5000) r(i))
SELECT
  CAST(i + 1 AS BIGINT) AS id,
  CASE WHEN p < 3 THEN 'vip' WHEN p < 500 THEN 'closed' WHEN p < 2000 THEN 'paused' ELSE 'active' END AS status,
  CASE WHEN p < 3 THEN 'board' WHEN p < 500 THEN 'ops'
       WHEN p < 2000 THEN ['sales', 'hr'][i % 2 + 1] ELSE ['sales', 'ops', 'it'][i % 3 + 1] END AS dept,
  CAST(CASE WHEN p < 3 THEN 90000 WHEN p < 500 THEN 20000 WHEN p < 2000 THEN 30000 ELSE 50000 END
       + ((i * 31) % 1000) * 10 AS DOUBLE) AS salary,
  CASE i + 1 WHEN 11 THEN 'Alex' WHEN 12 THEN 'Blair' WHEN 13 THEN 'Casey'
       ELSE ['Anna', 'Bernd', 'Clara', 'Dieter', 'Erika', 'Frieda', 'Gustav', 'Heinz'][i % 8 + 1] END AS first_name,
  CASE i + 1 WHEN 11 THEN 'Adams' WHEN 12 THEN 'Brooks' WHEN 13 THEN 'Carter'
       ELSE ['Schmidt', 'Mueller', 'Schulz', 'Wagner', 'Becker', 'Hoffmann', 'Koch', 'Richter'][(i // 8) % 8 + 1] END AS last_name,
  ['Hamburg', 'Bremen', 'Kiel', 'Essen'][i % 4 + 1] AS city,
  DATE '2020-01-01' + CAST((i * 37) % 1500 AS INTEGER) AS signup,
  CASE WHEN i % 10 = 0 THEN NULL ELSE 'c' || i || '@example.com' END AS email
FROM b;
CREATE TABLE crm.orders AS
WITH c AS (SELECT CAST(i AS BIGINT) AS cid, CASE WHEN i = 42 THEN 400 ELSE i % 7 END AS n FROM range(1, 5001) r(i)),
k AS (SELECT cid, unnest(range(n)) AS j FROM c UNION ALL SELECT 5000 + i, 0 FROM range(1, 4) r(i)),
o AS (SELECT row_number() OVER (ORDER BY cid, j) AS id, cid FROM k)
SELECT
  CAST(id AS BIGINT) AS id,
  cid AS customer_id,
  ['new', 'paid', 'shipped', 'returned'][id % 4 + 1] AS status,
  CAST(1 + (id * 13) % 20 AS INTEGER) AS quantity,
  CAST(5 + ((id * 17) % 1000) / 10 AS DECIMAL(10,2)) AS unit_price,
  TIMESTAMP '2023-01-01 00:00:00' + to_seconds((id * 7919) % 31536000) AS ordered_at,
  CASE WHEN id % 20 = 0 THEN NULL
       ELSE TIMESTAMP '2023-01-01 00:00:00' + to_seconds((id * 7919) % 31536000 + 86400 + (id * 104729) % (9 * 86400)) END AS shipped_at
FROM o;
`

var relFixture struct {
	once sync.Once
	path string
	err  error
}

func relationsPath(t *testing.T) string {
	t.Helper()
	relFixture.once.Do(func() {
		relFixture.path = filepath.Join(fixtureDir, "relations.duckdb")
		c, err := duckdb.NewConnector(relFixture.path, nil)
		if err != nil {
			relFixture.err = err
			return
		}
		db := sql.OpenDB(c)
		defer func() { _ = db.Close() }()
		_, relFixture.err = db.Exec(relationsSQL)
	})
	if relFixture.err != nil {
		t.Fatalf("relations fixture: %v", relFixture.err)
	}
	return relFixture.path
}

// relQuery opens an in-memory DuckDB with the relations source as src
// and the snapshot as syn.
func relQuery(t *testing.T, snapshot string) *sql.DB {
	t.Helper()
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		"ATTACH '" + relationsPath(t) + "' AS src (READ_ONLY)",
		"ATTACH '" + snapshot + "' AS syn (READ_ONLY)",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func relSpec(cols map[string]apitypes.SyntheticColumn) *apitypes.SyntheticSpec {
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{{
			Name:    "crm.customers",
			Size:    apitypes.SyntheticSize{Ratio: f64(0.2)},
			Columns: cols,
		}},
	}
}

// R9.
func TestTemplateColumns(t *testing.T) {
	spec := relSpec(map[string]apitypes.SyntheticColumn{
		"first_name": {Generator: apitypes.GenFake, Args: map[string]any{"kind": "first_name", "nullRate": 0.1}},
		"last_name":  {Generator: apitypes.GenFake, Args: map[string]any{"kind": "last_name"}},
		// The source email has 10 % NULLs; the template ignores them.
		"email": {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{First_Name}.{last_name}@Example.org", "lower": true}},
		// dept comes before both refs in the table, so it must be drawn after them.
		"dept": {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{city} {first_name}"}},
	})
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: spec, dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	for _, q := range []string{
		"SELECT count(*) FROM syn.crm.customers WHERE email IS DISTINCT FROM lower(first_name || '.' || last_name || '@example.org')",
		"SELECT count(*) FROM syn.crm.customers WHERE (email IS NULL) <> (first_name IS NULL OR last_name IS NULL)",
		"SELECT count(*) FROM syn.crm.customers WHERE dept IS DISTINCT FROM city || ' ' || first_name",
	} {
		if n := scalar[int64](t, db, q); n != 0 {
			t.Errorf("%d rows: %s", n, q)
		}
	}
	nulls := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE email IS NULL")
	firstNulls := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE first_name IS NULL")
	if nulls == 0 || nulls != firstNulls {
		t.Errorf("email NULLs = %d, first_name NULLs = %d", nulls, firstNulls)
	}

	want := map[string][]string{"email": {"first_name", "last_name"}, "dept": {"city", "first_name"}}
	for _, c := range rep.Tables[0].Columns {
		if !slices.Equal(c.DependsOn, want[c.Name]) {
			t.Errorf("column %s: depends_on = %v, want %v", c.Name, c.DependsOn, want[c.Name])
		}
		if want[c.Name] != nil && (c.Generator != "template" || c.Distance != nil || c.Source == nil) {
			t.Errorf("column %s report = %+v", c.Name, c)
		}
	}
}

// A template on a column whose stage is a DOUBLE shows the value the
// snapshot holds, not the staged double.
func TestTemplateCastRefs(t *testing.T) {
	spec := baseSpec()
	cols := spec.Tables[0].Columns
	cols["revenue"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution}
	cols["notes"] = apitypes.SyntheticColumn{Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{revenue}|{opened}"}}
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: spec, dir: dir})
	db := query(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	q := "SELECT count(*) FROM syn.crm.customers WHERE notes IS DISTINCT FROM CAST(revenue AS VARCHAR) || '|' || CAST(opened AS VARCHAR)"
	if n := scalar[int64](t, db, q); n != 0 {
		t.Errorf("%d rows: %s", n, q)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.customers WHERE CAST(opened AS VARCHAR) LIKE '%.%'"); n == 0 {
		t.Error("no TIME value with a fraction of a second")
	}
}

type stageFunc func(generate.RowCtx) (any, error)

func (f stageFunc) Next(row generate.RowCtx) (any, error) { return f(row) }
func (stageFunc) Pool() int64                             { return -1 }

// The row hash runs in ordinal order, also when the columns are drawn in
// another order.
func TestStageHashOrdinal(t *testing.T) {
	c, err := duckdb.NewConnector("", nil)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Column 0 reads column 1, so column 1 is drawn first.
	cols := []*column{
		{stage: generate.StageVarchar, gen: stageFunc(func(r generate.RowCtx) (any, error) {
			return fmt.Sprint("x", r.Values[1]), nil
		})},
		{stage: generate.StageBigint, gen: stageFunc(func(r generate.RowCtx) (any, error) { return r.Index, nil })},
	}
	b := &builder{hash: sha256.New(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	const rows = 5
	if err := b.stage(ctx, conn, cols, []int{1, 0}, rows); err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	for i := range int64(rows) {
		for _, v := range []any{fmt.Sprint("x", i), i} {
			if err := hashValue(want, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !bytes.Equal(b.hash.Sum(nil), want.Sum(nil)) {
		t.Error("stage hash is not in ordinal order")
	}
}

func TestTemplateUnknownColumn(t *testing.T) {
	spec := relSpec(map[string]apitypes.SyntheticColumn{
		"email": {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{nope}@x"}},
		"dept":  {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{City}"}},
		"city":  {Generator: apitypes.GenTemplate, Args: map[string]any{"template": "{gone}"}},
	})
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	if err == nil {
		t.Fatal("want an error")
	}
	// Both errors come back together.
	for _, want := range []string{
		"column email: template names column nope, which is not in the table",
		"column city: template names column gone, which is not in the table",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not hold %q", err, want)
		}
	}
}

func ordersSpec(cols map[string]apitypes.SyntheticColumn) *apitypes.SyntheticSpec {
	spec := relSpec(cols)
	spec.Tables[0].Name = "crm.orders"
	return spec
}

// R8.
func TestDateOffsetColumns(t *testing.T) {
	spec := ordersSpec(map[string]apitypes.SyntheticColumn{
		"ordered_at": {Generator: apitypes.GenDateDistribution, Args: map[string]any{"nullRate": 0.1}},
		"shipped_at": {Generator: apitypes.GenDateOffset, Args: map[string]any{"from": "Ordered_At"}},
	})
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: spec, dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	if n, m := scalar[int64](t, db, "SELECT count(*) FROM src.crm.orders"), scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders"); n != 15400 || m != 3080 {
		t.Fatalf("orders: %d source rows, %d synthetic rows", n, m)
	}
	const off = "epoch(shipped_at) - epoch(ordered_at)"
	for _, q := range []string{
		"SELECT count(*) FROM syn.crm.orders WHERE shipped_at < ordered_at",
		"SELECT count(*) FROM syn.crm.orders WHERE ordered_at IS NULL AND shipped_at IS NOT NULL",
		"SELECT count(*) FROM syn.crm.orders WHERE " + off + " <> round(" + off + ")",
	} {
		if n := scalar[int64](t, db, q); n != 0 {
			t.Errorf("%d rows: %s", n, q)
		}
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders WHERE ordered_at IS NULL"); n == 0 {
		t.Error("no NULL ordered_at, nullRate 0.1 shows nothing")
	}

	// The null share of shipped_at counts the rows with an ordered_at.
	const nullShare = "SELECT count(*) FILTER (WHERE shipped_at IS NULL) / count(*) FROM %s.crm.orders WHERE ordered_at IS NOT NULL"
	srcNull := scalar[float64](t, db, fmt.Sprintf(nullShare, "src"))
	synNull := scalar[float64](t, db, fmt.Sprintf(nullShare, "syn"))
	if math.Abs(srcNull-synNull) > 0.02 {
		t.Errorf("shipped_at null share %v, source %v", synNull, srcNull)
	}

	const stats = "SELECT min(" + off + "), max(" + off + "), median(" + off + ") FROM %s.crm.orders"
	var srcMin, srcMax, srcMed, synMin, synMax, synMed float64
	if err := db.QueryRow(fmt.Sprintf(stats, "src")).Scan(&srcMin, &srcMax, &srcMed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(fmt.Sprintf(stats, "syn")).Scan(&synMin, &synMax, &synMed); err != nil {
		t.Fatal(err)
	}
	if r := srcMax - srcMin; math.Abs(synMed-srcMed) > 0.05*r {
		t.Errorf("median offset %v, source %v, range %v", synMed, srcMed, r)
	}
	if synMax == srcMax || synMin == srcMin {
		t.Errorf("offset bounds %v %v equal a source bound %v %v", synMin, synMax, srcMin, srcMax)
	}

	var found bool
	for _, c := range rep.Tables[0].Columns {
		if c.Name != "shipped_at" {
			continue
		}
		found = true
		if !slices.Equal(c.DependsOn, []string{"ordered_at"}) || c.Distance == nil || c.Distance.Metric != "ks" || c.Distance.Value > 0.1 {
			t.Errorf("shipped_at report: depends_on %v, distance %+v", c.DependsOn, c.Distance)
		}
	}
	if !found {
		t.Error("no shipped_at in the report")
	}
}

func TestDateOffsetErrors(t *testing.T) {
	spec := ordersSpec(map[string]apitypes.SyntheticColumn{
		"status":     {Generator: apitypes.GenDateOffset, Args: map[string]any{"from": "ordered_at"}},
		"shipped_at": {Generator: apitypes.GenDateOffset, Args: map[string]any{"from": "nope"}},
		"ordered_at": {Generator: apitypes.GenDateOffset, Args: map[string]any{"from": "quantity"}},
	})
	_, err := run(t, opts{spec: spec, source: relationsPath(t)})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		"column status: generator does not fit the column type: dateOffset on VARCHAR",
		"column shipped_at: dateOffset names column nope, which is not in the table",
		"column ordered_at: dateOffset needs a TIMESTAMP column in from, quantity has type INTEGER",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not hold %q", err, want)
		}
	}
}
