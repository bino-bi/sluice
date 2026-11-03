// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// fullSpec uses every relation feature on both relations tables.
func fullSpec() *apitypes.SyntheticSpec {
	spec := exactSpec("first_name", "last_name")
	cust := spec.Tables[0].Columns
	cust["id"] = sequenceID
	cust["status"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	cust["salary"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution, Args: map[string]any{"by": []any{"status"}}}
	cust["dept"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical, Args: map[string]any{"by": []any{"status"}}}
	spec.Tables = append(spec.Tables, apitypes.SyntheticTable{
		Name: "crm.orders",
		Size: apitypes.SyntheticSize{Ratio: f64(0.2)},
		Columns: map[string]apitypes.SyntheticColumn{
			"customer_id": {Generator: apitypes.GenForeignKey, Args: map[string]any{"table": "crm.customers", "column": "id", "orphanRate": "source"}},
			"shipped_at":  {Generator: apitypes.GenDateOffset, Args: map[string]any{"from": "ordered_at"}},
			"total":       {Generator: apitypes.GenExpression, Args: map[string]any{"sql": "quantity * unit_price"}},
			"notes":       {Generator: apitypes.GenSourceVocabulary},
		},
	})
	return spec
}

// R15: no source marker value reaches the log at info and above, the
// error or the report, for passing and failing builds.
func TestNoLeaks(t *testing.T) {
	failFK := fullSpec()
	failFK.Tables[0].Columns["id"] = apitypes.SyntheticColumn{Generator: apitypes.GenDistribution}
	failExact := fullSpec()
	failExact.Tables[0].Privacy.RejectExactMatches = []string{"city", "status"}
	failExact.Tables[0].Columns["city"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	failExact.Tables[0].Columns["status"] = apitypes.SyntheticColumn{Generator: apitypes.GenCategorical}
	failExpr := fullSpec()
	failExpr.Tables[1].Columns["total"] = apitypes.SyntheticColumn{Generator: apitypes.GenExpression,
		Args: map[string]any{"sql": "CASE WHEN status = 'vip' THEN (SELECT max(city) FROM crm.customers) END"}}

	markers := []string{"zebulon", "vip", "hamburg", "bremen", "kiel", "schmidt", "mueller", "anna", "bernd", "@example.com"}
	for _, tc := range []struct {
		name string
		spec *apitypes.SyntheticSpec
		want string // part of the error, empty for a passing build
	}{
		{"passing", fullSpec(), ""},
		{"foreign key", failFK, "foreignKey needs unique values"},
		{"exact matches", failExact, "rows equal a source row on city, status"},
		{"expression", failExpr, "expression uses a subquery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			dir := t.TempDir()
			rep, err := run(t, opts{spec: tc.spec, dir: dir, source: relationsPath(t), log: log})
			if (err == nil) != (tc.want == "") || err != nil && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			report, rerr := os.ReadFile(Layout{Dir: dir, Name: "crmsynth"}.Report(rep.BuildID))
			if rerr != nil {
				t.Fatal(rerr)
			}
			text := buf.String() + string(report)
			if err != nil {
				text += err.Error()
			}
			text = strings.ToLower(text)
			for _, m := range markers {
				if strings.Contains(text, m) {
					t.Errorf("%q found in the log, report or error", m)
				}
			}
		})
	}
}
