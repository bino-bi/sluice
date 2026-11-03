// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"testing"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// fkSpec lists orders before customers, so the build must reorder them.
func fkSpec(fk map[string]any, customerID apitypes.SyntheticColumn) *apitypes.SyntheticSpec {
	args := map[string]any{"table": "crm.customers", "column": "id"}
	for k, v := range fk {
		args[k] = v
	}
	return &apitypes.SyntheticSpec{
		Source:  "crm",
		Seed:    seedRef,
		Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: intp(5)},
		Tables: []apitypes.SyntheticTable{
			{
				Name: "crm.orders",
				Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
				Columns: map[string]apitypes.SyntheticColumn{
					"customer_id": {Generator: apitypes.GenForeignKey, Args: args},
				},
			},
			{
				Name: "crm.customers",
				Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
				Columns: map[string]apitypes.SyntheticColumn{
					"id":    customerID,
					"email": {Generator: apitypes.GenFake, Args: map[string]any{"kind": "email"}},
				},
			},
		},
	}
}

func ordersColumn(t *testing.T, rep *Report, name string) ColumnReport {
	t.Helper()
	for _, tr := range rep.Tables {
		for _, c := range tr.Columns {
			if tr.Name == "crm.orders" && c.Name == name {
				return c
			}
		}
	}
	t.Fatalf("no column crm.orders.%s in the report", name)
	return ColumnReport{}
}

var sequenceID = apitypes.SyntheticColumn{Generator: apitypes.GenSequence}

const orphanQuery = `SELECT count(*) FROM syn.crm.orders o WHERE o.customer_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM syn.crm.customers c WHERE c.id = o.customer_id)`

// R2, R3 and R4.
func TestForeignKeyBuild(t *testing.T) {
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: fkSpec(nil, sequenceID), dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))

	// R2: parents first in the report.
	if len(rep.Tables) != 2 || rep.Tables[0].Name != "crm.customers" || rep.Tables[1].Name != "crm.orders" {
		t.Fatalf("tables = %+v", rep.Tables)
	}
	if rep.Tables[0].DependsOn != nil || !slices.Equal(rep.Tables[1].DependsOn, []string{"crm.customers"}) {
		t.Errorf("depends_on: customers %v, orders %v", rep.Tables[0].DependsOn, rep.Tables[1].DependsOn)
	}

	// R3: every key has its parent.
	if n := scalar[int64](t, db, orphanQuery); n != 0 {
		t.Errorf("%d orders without a customer", n)
	}
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders WHERE customer_id IS NULL"); n != 0 {
		t.Errorf("%d NULL keys, the source has none", n)
	}

	// R4: shape and privacy of the fan-out.
	cr := ordersColumn(t, rep, "customer_id")
	f := cr.FanOut
	if f == nil || f.Parent != "crm.customers.id" || f.Parents != 1000 || f.OrphanRows != 0 {
		t.Fatalf("fan_out = %+v", f)
	}
	if f.Scale < 0.9 || f.Scale > 1.1 {
		t.Errorf("scale = %v, want about 1.03", f.Scale)
	}
	if cr.Distance == nil || cr.Distance.Metric != "tv" || cr.Distance.Value > 0.1 {
		t.Errorf("distance = %+v", cr.Distance)
	}
	if cr.MergedBins == 0 {
		t.Error("merged_bins = 0, the 400-order customer must be merged")
	}
	// maxBin is 6 after the merge.
	bound := int64(math.Ceil(6 * f.Scale))
	maxFan := scalar[int64](t, db, "SELECT max(n) FROM (SELECT count(*) AS n FROM syn.crm.orders GROUP BY customer_id)")
	if maxFan > bound || maxFan >= 50 {
		t.Errorf("largest fan-out %d, bound %d", maxFan, bound)
	}
}

// R3 with orphans.
func TestForeignKeyOrphanRate(t *testing.T) {
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: fkSpec(map[string]any{"orphanRate": 0.05}, sequenceID), dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	orphans := scalar[int64](t, db, orphanQuery)
	rows := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders")
	if share := float64(orphans) / float64(rows); math.Abs(share-0.05) > 0.01 {
		t.Errorf("orphan share %v (%d of %d)", share, orphans, rows)
	}
	if f := ordersColumn(t, rep, "customer_id").FanOut; f == nil || f.OrphanRows != orphans {
		t.Errorf("fan_out = %+v, %d orphans in the snapshot", f, orphans)
	}
	// Orphan keys are new values, above every parent key.
	if n := scalar[int64](t, db, "SELECT count(*) FROM syn.crm.orders WHERE customer_id <= (SELECT max(id) FROM syn.crm.customers) AND customer_id NOT IN (SELECT id FROM syn.crm.customers)"); n != 0 {
		t.Errorf("%d orphan keys inside the parent key range", n)
	}
}

// R5.
func TestForeignKeyErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec *apitypes.SyntheticSpec
		want string
	}{
		{"not unique", fkSpec(nil, apitypes.SyntheticColumn{Generator: apitypes.GenDistribution}),
			"table crm.orders: column customer_id: foreignKey needs unique values in crm.customers.id (sequence, uuid or unique: true)"},
		{"type", func() *apitypes.SyntheticSpec {
			s := fkSpec(nil, sequenceID)
			s.Tables[0].Columns["status"] = s.Tables[0].Columns["customer_id"]
			delete(s.Tables[0].Columns, "customer_id")
			return s
		}(), "table crm.orders: column status: foreignKey type does not match crm.customers.id"},
		{"missing column", fkSpec(map[string]any{"column": "nope"}, sequenceID),
			"table crm.orders: column customer_id: foreignKey names column nope, which is not in crm.customers"},
		{"no keys", func() *apitypes.SyntheticSpec {
			s := fkSpec(nil, sequenceID)
			s.Tables[1].Size = apitypes.SyntheticSize{Rows: i64(0)}
			return s
		}(), "table crm.orders: column customer_id: foreignKey parent crm.customers has no keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, opts{spec: tc.spec, source: relationsPath(t)})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// The source fan-out is above 1, so a fixed fan-out of 0 or 1 scales by
// more than 2.
func TestForeignKeyScaleWarning(t *testing.T) {
	spec := fkSpec(map[string]any{"fanOut": map[string]any{"min": 0.0, "max": 1.0}}, sequenceID)
	rep, _ := mustRun(t, opts{spec: spec, dir: t.TempDir(), source: relationsPath(t)})
	cr := ordersColumn(t, rep, "customer_id")
	if cr.FanOut == nil || cr.FanOut.Scale <= 2 {
		t.Fatalf("fan_out = %+v, want a scale above 2", cr.FanOut)
	}
	want := fmt.Sprintf("the fan-outs are scaled by %.2f to fit the row count, the parent and child sizes do not fit the source fan-out", cr.FanOut.Scale)
	if !slices.Contains(cr.Warnings, want) || !slices.Contains(rep.Warnings, "crm.orders.customer_id: "+want) {
		t.Errorf("warnings = %q, report warnings = %q", cr.Warnings, rep.Warnings)
	}
}

type fakeFK struct{ orphans int64 }

func (fakeFK) Next(generate.RowCtx) (any, error) { return nil, nil }
func (fakeFK) Pool() int64                       { return -1 }
func (f fakeFK) FanOutStats() *generate.FanOutStats {
	return &generate.FanOutStats{Orphans: f.orphans}
}

// The check after the write must fail when the orphans in the table do
// not match the orphans drawn.
func TestVerifyForeignKeys(t *testing.T) {
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
	t.Cleanup(func() { _ = conn.Close() })
	for _, s := range []string{
		"ATTACH ':memory:' AS " + outCatalog,
		"CREATE SCHEMA " + outCatalog + ".crm",
		"CREATE TABLE " + outCatalog + ".crm.customers AS SELECT * FROM range(1, 4) t(id)",
		"CREATE TABLE " + outCatalog + ".crm.orders AS SELECT * FROM (VALUES (1), (3), (NULL), (7)) t(customer_id)",
	} {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	b := &builder{log: slog.New(slog.DiscardHandler), keys: newKeyCache(fkSpec(nil, sequenceID))}
	b.keys.keys["crm.customers"] = map[string]*keyEntry{"id": {table: "crm.customers", column: "id"}}
	target := outCatalog + ".crm.orders"
	col := func(orphans int64) []*column {
		return []*column{{
			info:   profile.ColumnInfo{Name: "customer_id"},
			choice: generate.Choice{Generator: apitypes.GenForeignKey, Args: map[string]any{"table": "crm.customers", "column": "id"}},
			gen:    fakeFK{orphans: orphans},
		}}
	}
	if err := b.verifyForeignKeys(ctx, conn, target, col(1)); err != nil {
		t.Errorf("one orphan drawn, one in the table: %v", err)
	}
	for _, n := range []int64{0, 2} {
		err := b.verifyForeignKeys(ctx, conn, target, col(n))
		if err == nil || err.Error() != "foreignKey check failed for column customer_id" {
			t.Errorf("%d orphans drawn, one in the table: err = %v", n, err)
		}
	}
}
