// SPDX-License-Identifier: AGPL-3.0-or-later

package queryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/queryservice"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// A parse error that surfaces after an allow must carry the same code in
// the audit record and in the client error. The raw sentinel maps to
// ERR_INTERNAL unless the mapping runs first.
func TestExecute_ParseErrorAuditCodeMatchesClient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code pkgerr.Code
	}{
		{"too deep", parser.ErrTooDeep, pkgerr.CodeUnsupportedSyntax},
		{"identifier too long", parser.ErrIdentifierTooLong, pkgerr.CodeUnsupportedSyntax},
		{"syntax", &parser.ParseError{Cause: parser.ErrSyntax}, pkgerr.CodeSyntax},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAudit{}
			svc := queryservice.New(queryservice.Options{
				Parser:   &fakeParser{err: tc.err},
				Policy:   &countingPolicy{dec: &policy.Decision{Outcome: policy.OutcomeAllow}},
				Rewriter: &fakeRewriter{},
				Executor: &fakeExecutor{},
				Audit:    a,
				Clock:    func() time.Time { return time.Unix(1713600000, 0) },
			})
			_, err := svc.Execute(context.Background(), queryservice.QueryRequest{SQL: "SELECT 1 FROM t"})
			ae := pkgerr.FromError(err)
			if ae == nil || ae.Code != tc.code {
				t.Fatalf("client code = %v, want %s", ae, tc.code)
			}
			recs := a.all()
			if len(recs) != 1 {
				t.Fatalf("want 1 audit record, got %d", len(recs))
			}
			if recs[0].Decision != audit.DecisionError {
				t.Fatalf("decision = %s, want error", recs[0].Decision)
			}
			if recs[0].ErrorCode != string(tc.code) {
				t.Fatalf("audit error_code = %q, client code = %q", recs[0].ErrorCode, tc.code)
			}
		})
	}
}
