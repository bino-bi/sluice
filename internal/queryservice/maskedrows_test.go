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
			SQL:     "SELECT id, email FROM t",
			Changed: true,
			PostMasks: []rewriter.PostMask{{
				ColumnIndex: 1,
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
			SQL:     "SELECT ssn FROM t",
			Changed: true,
			PostMasks: []rewriter.PostMask{{
				ColumnIndex: 0,
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

	if _, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT ssn FROM t"}); err == nil {
		t.Fatal("expected error when mask build fails, got nil")
	}
	for _, r := range a.all() {
		if r.Decision == "allow" {
			t.Error("query audited as allow despite mask build failure")
		}
	}
}
