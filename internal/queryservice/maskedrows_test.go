// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// keyStore satisfies pkg/mask KeyStore + SaltStore for the tests.
type keyStore map[string][]byte

func (k keyStore) Get(_ context.Context, ref string) ([]byte, error) { return k[ref], nil }

func TestExecute_PostQueryMaskAppliesToResultColumn(t *testing.T) {
	a := &fakeAudit{}
	ex := &fakeExecutor{
		columns: []executor.ColumnInfo{{Name: "id"}, {Name: "email"}},
		rows:    [][]any{{int64(1), "alice@example.com"}, {int64(2), "bob@example.com"}},
	}
	svc := queryservice.New(queryservice.Options{
		Parser: &fakeParser{},
		Policy: &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{result: &rewriter.RewriteResult{
			SQL:               "SELECT id, email FROM t",
			Changed:           true,
			ResultColumnCount: 2,
			PostMasks: []rewriter.PostMask{{
				ColumnIndex: 1,
				OutputName:  "email",
				TableKey:    "pg.hr.employees",
				Column:      "email",
				Type:        apitypes.MaskFake,
				Args:        pkgmask.Args{FakeType: "email", Seed: "s"},
				Policy:      "fake-email",
			}},
		}},
		Executor: ex,
		Audit:    a,
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
		Salts:    keyStore{},
		Keys:     keyStore{},
	})

	res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id, email FROM t"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = res.Rows.Close() }()

	var got [][2]any
	for res.Rows.Next() {
		var id, email any
		if err := res.Rows.Scan(&id, &email); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [2]any{id, email})
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	for _, row := range got {
		if row[1] == "alice@example.com" || row[1] == "bob@example.com" {
			t.Errorf("email column not masked: %v", row[1])
		}
		if s, ok := row[1].(string); !ok || s == "" {
			t.Errorf("masked email not a non-empty string: %v", row[1])
		}
	}
	// id column untouched.
	if got[0][0] != int64(1) {
		t.Errorf("id column altered: %v", got[0][0])
	}

	// Audit record notes the post-mask.
	recs := a.all()
	if len(recs) == 0 {
		t.Fatal("no audit record")
	}
	if _, ok := recs[0].Extras["post_masks"]; !ok {
		t.Errorf("audit extras missing post_masks: %+v", recs[0].Extras)
	}
}

// A post mask is bound to a result column index. When DuckDB returns
// another shape than the rewriter planned (unnest of a struct adds
// columns), the index points at the wrong column and the masked column
// would be served raw. The query must be refused with no row served.
func TestExecute_PostQueryMaskResultShapeMismatchRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		columns []executor.ColumnInfo
		sampled bool
	}{
		"extra column":            {columns: []executor.ColumnInfo{{Name: "a"}, {Name: "b"}, {Name: "email"}}},
		"other name at the index": {columns: []executor.ColumnInfo{{Name: "id"}, {Name: "other"}}},
		"renamed, not sampled":    {columns: []executor.ColumnInfo{{Name: "id"}, {Name: "email_1"}}},
		"sampled, other suffix":   {columns: []executor.ColumnInfo{{Name: "id"}, {Name: "email_x"}}, sampled: true},
	} {
		t.Run(name, func(t *testing.T) {
			a := &fakeAudit{}
			row := make([]any, len(tc.columns))
			for i := range row {
				row[i] = "alice@example.com"
			}
			ex := &fakeExecutor{columns: tc.columns, rows: [][]any{row}}
			svc := queryservice.New(queryservice.Options{
				Parser: &fakeParser{},
				Policy: &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{result: &rewriter.RewriteResult{
					SQL:               "SELECT id, email FROM t",
					Changed:           true,
					ResultColumnCount: 2,
					Sampled:           tc.sampled,
					PostMasks: []rewriter.PostMask{{
						ColumnIndex: 1,
						OutputName:  "email",
						TableKey:    "pg.hr.employees",
						Column:      "email",
						Type:        apitypes.MaskFake,
						Args:        pkgmask.Args{FakeType: "email", Seed: "s"},
						Policy:      "fake-email",
					}},
				}},
				Executor: ex,
				Audit:    a,
				Clock:    func() time.Time { return time.Unix(1713600000, 0) },
				Salts:    keyStore{},
				Keys:     keyStore{},
			})

			res, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT id, email FROM t"})
			if err == nil {
				t.Fatalf("served %v, want ERR_MASK_UNSUPPORTED_CONTEXT", drainRows(t, res))
			}
			if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeMaskContext {
				t.Fatalf("code = %s, want ERR_MASK_UNSUPPORTED_CONTEXT", ae.Code)
			}
			for _, r := range a.all() {
				if r.Decision == "allow" {
					t.Error("query audited as allow")
				}
			}
		})
	}
}

func TestExecute_PostQueryMaskBuildFailureRefusesQuery(t *testing.T) {
	a := &fakeAudit{}
	ex := &fakeExecutor{
		columns: []executor.ColumnInfo{{Name: "ssn"}},
		rows:    [][]any{{"123-45-6789"}},
	}
	// FPE with no key store configured — NewRowMask fails, and the query
	// must be refused before any row is served or audited as allowed.
	svc := queryservice.New(queryservice.Options{
		Parser: &fakeParser{},
		Policy: &fakePolicy{decision: &policy.Decision{Outcome: policy.OutcomeAllow}},
		Rewriter: &fakeRewriter{result: &rewriter.RewriteResult{
			SQL:               "SELECT ssn FROM t",
			Changed:           true,
			ResultColumnCount: 1,
			PostMasks: []rewriter.PostMask{{
				ColumnIndex: 0,
				OutputName:  "ssn",
				TableKey:    "pg.hr.employees",
				Column:      "ssn",
				Type:        apitypes.MaskFPE,
				Args:        pkgmask.Args{KeyRef: "secret://env/K", Alphabet: "numeric"},
				Policy:      "fpe-ssn",
			}},
		}},
		Executor: ex,
		Audit:    a,
		Clock:    func() time.Time { return time.Unix(1713600000, 0) },
	})

	_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"})
	if err == nil {
		t.Fatal("expected error when mask build fails, got nil")
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeInternal {
		t.Fatalf("code = %s (%s), want ERR_INTERNAL from the mask build", ae.Code, ae.Message)
	}
	for _, r := range a.all() {
		if r.Decision == "allow" {
			t.Error("query audited as allow despite mask build failure")
		}
	}
}
