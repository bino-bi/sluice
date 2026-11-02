// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"errors"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

func TestEndpointSampleWrap(t *testing.T) {
	const in = `SELECT id FROM t`
	stratPrefix := `SELECT * EXCLUDE (sluice_h, sluice_rn, sluice_n, sluice_t) FROM (SELECT *, `
	cases := []struct {
		name     string
		spec     SampleSpec
		wantSQL  string
		wantNote string
	}{
		{
			name:     "reservoir",
			spec:     SampleSpec{Method: apitypes.SampleMethodReservoir, Rows: 500, Seed: 42},
			wantSQL:  `SELECT * FROM (SELECT id FROM t) AS sluice_endpoint_sample USING SAMPLE 500 ROWS (reservoir, 42)`,
			wantNote: "sample-endpoint:reservoir:500",
		},
		{
			name:     "bernoulli",
			spec:     SampleSpec{Method: apitypes.SampleMethodBernoulli, Rate: 0.05, Seed: 0},
			wantSQL:  `SELECT * FROM (SELECT id FROM t) AS sluice_endpoint_sample USING SAMPLE 5% (bernoulli, 0)`,
			wantNote: "sample-endpoint:bernoulli:5%",
		},
		{
			name:     "system",
			spec:     SampleSpec{Method: apitypes.SampleMethodSystem, Rate: 1, Seed: MaxSampleSeed},
			wantSQL:  `SELECT * FROM (SELECT id FROM t) AS sluice_endpoint_sample USING SAMPLE 100% (system, 2147483647)`,
			wantNote: "sample-endpoint:system:100%",
		},
		{
			name: "stratified rate",
			spec: SampleSpec{Method: apitypes.SampleMethodStratified, Rate: 0.1, Seed: 7, StratifyBy: []string{"kind"}, MinGroupRows: 10},
			wantSQL: stratPrefix +
				`row_number() OVER (PARTITION BY "kind" ORDER BY sluice_h) AS sluice_rn, ` +
				`count(*) OVER (PARTITION BY "kind") AS sluice_n, count(*) OVER () AS sluice_t ` +
				`FROM (SELECT sluice_x.*, hash(sluice_x, 7) AS sluice_h FROM (SELECT id FROM t) AS sluice_x) AS sluice_y) AS sluice_ranked ` +
				`WHERE sluice_n >= 10 AND sluice_rn <= ceil(sluice_n * 0.1) ORDER BY sluice_rn, sluice_h`,
			wantNote: "sample-endpoint:stratified:10%",
		},
		{
			name: "stratified rows",
			spec: SampleSpec{Method: apitypes.SampleMethodStratified, Rows: 50, Seed: 7, StratifyBy: []string{"a", `b"c`}},
			wantSQL: stratPrefix +
				`row_number() OVER (PARTITION BY "a", "b""c" ORDER BY sluice_h) AS sluice_rn, ` +
				`count(*) OVER (PARTITION BY "a", "b""c") AS sluice_n, count(*) OVER () AS sluice_t ` +
				`FROM (SELECT sluice_x.*, hash(sluice_x, 7) AS sluice_h FROM (SELECT id FROM t) AS sluice_x) AS sluice_y) AS sluice_ranked ` +
				`WHERE sluice_n >= 0 AND sluice_rn <= ceil(sluice_n * (50::DOUBLE / sluice_t)) ORDER BY sluice_rn, sluice_h LIMIT 50`,
			wantNote: "sample-endpoint:stratified:50",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, note := endpointSampleWrap(in, &tc.spec)
			if sql != tc.wantSQL {
				t.Errorf("SQL\n got %s\nwant %s", sql, tc.wantSQL)
			}
			if note != tc.wantNote {
				t.Errorf("note = %q, want %q", note, tc.wantNote)
			}
		})
	}
}

func TestCheckSampleStratify(t *testing.T) {
	masks := []PostMask{{Column: "total", Type: apitypes.MaskJitter}}
	s := &state{sample: &SampleSpec{Method: apitypes.SampleMethodStratified}, postMasks: masks}
	err := s.checkSampleStratify()
	var ae *pkgerr.APIError
	if !errors.As(err, &ae) || ae.Code != pkgerr.CodeMaskContext {
		t.Fatalf("stratified with post mask: err = %v, want %s", err, pkgerr.CodeMaskContext)
	}
	s.sample.Method = apitypes.SampleMethodReservoir
	if err := s.checkSampleStratify(); err != nil {
		t.Errorf("reservoir with post mask: %v", err)
	}
	s.sample.Method = apitypes.SampleMethodStratified
	s.postMasks = nil
	if err := s.checkSampleStratify(); err != nil {
		t.Errorf("stratified without post mask: %v", err)
	}
}

func TestCheckSampleStatement(t *testing.T) {
	const base = `SELECT "a", "b" FROM "c"."s"."t"`
	accept := []string{
		base,
		base + ` WHERE (a > 1)`,
		base + ` WHERE (a IN (SELECT id FROM c.s.u WHERE x = 1))`,
		`SELECT * FROM "c"."s"."t" WHERE (true)`,
	}
	refuse := []string{
		base + ` WHERE (1=1) UNION SELECT a, b FROM c.s.u WHERE (1=1)`,
		base + ` WHERE (1=1) ORDER BY a`,
		base + ` WHERE (1=1) LIMIT 1`,
		base + ` WHERE (1=1) OFFSET 1`,
		base + ` WHERE (1=1) GROUP BY a, b`,
		base + ` WHERE (1=1) GROUP BY a, b HAVING count(*) > 1`,
		`SELECT DISTINCT "a" FROM "c"."s"."t"`,
		`WITH x AS (SELECT 1) SELECT "a" FROM "c"."s"."t"`,
		base + ` WHERE (1=1) FOR UPDATE`,
		base + `, c.s.u WHERE (1=1)`,
		`SELECT "a" FROM (SELECT a FROM c.s.t) AS x`,
		`SELECT 1; SELECT 2`,
	}
	for _, sql := range accept {
		res, err := pg.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if err := CheckSampleStatement(res); err != nil {
			t.Errorf("CheckSampleStatement(%q) = %v, want nil", sql, err)
		}
	}
	for _, sql := range refuse {
		res, err := pg.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		err = CheckSampleStatement(res)
		var ae *pkgerr.APIError
		if !errors.As(err, &ae) || ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("CheckSampleStatement(%q) = %v, want %s", sql, err, pkgerr.CodeUnsupportedSyntax)
		}
	}
}
