// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// tenantFilter filters tables on tenant_id = the subject's tenant_id claim.
func tenantFilter(tables ...string) *apitypes.RowFilterPolicy {
	return &apitypes.RowFilterPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
		Metadata: apitypes.ObjectMeta{Name: "tenant-filter"},
		Spec: apitypes.RowFilterSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tables: tables}},
			}},
			Filter: apitypes.FilterSpec{Predicate: &apitypes.Predicate{
				Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "{{ subject.tenant_id }}",
			}},
		},
	}
}

// A filtered table without an alias gets a wrapper named after the table,
// and pg_query deparses a lower-case name bare. DuckDB reads some names as
// a keyword: semi, anti, asof and positional change the join type and drop
// the wrapper's name, so the mask pass cannot follow it. Others break the
// statement. Such a table needs an alias.
func TestRewrite_WrapperAliasKeywordRefused(t *testing.T) {
	rw := buildRewriter(t)
	user := &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}
	filter := tenantFilter("semi", "by", "orders")
	for _, sql := range []string{
		`SELECT o.id FROM pg.public."semi" JOIN pg.public.orders o ON true`,
		`SELECT o.id FROM pg.public.semi JOIN pg.public.orders o ON true`,
		`SELECT count(*) FROM pg.public."by"`,
	} {
		dec, ast := evaluate(t, sql, user, allowAll(0), filter)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql})
		if err == nil {
			t.Errorf("%s: served %q, want ERR_UNSUPPORTED_SYNTAX", sql, res.SQL)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax || !strings.Contains(ae.Message, "alias") {
			t.Errorf("%s: err = %v, want ERR_UNSUPPORTED_SYNTAX asking for an alias", sql, err)
		}
	}
	// pg_query quotes a name with upper case, and DuckDB reads a quoted
	// name as an alias.
	for sql, wrapper := range map[string]string{
		`SELECT s.id FROM pg.public."semi" s JOIN pg.public.orders o ON true`: ") s ",
		`SELECT count(*) FROM pg.public.orders`:                               ") orders",
		`SELECT o.id FROM pg.public."Semi" JOIN pg.public.orders o ON true`:   `) "Semi" `,
		`SELECT count(*) FROM pg.public."BY"`:                                 `) "BY"`,
	} {
		dec, ast := evaluate(t, sql, user, allowAll(0), filter)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql})
		if err != nil {
			t.Errorf("%s: %v", sql, err)
		} else if !strings.Contains(res.SQL, wrapper) {
			t.Errorf("%s: SQL = %q, want a filter wrapper named %q", sql, res.SQL, wrapper)
		}
	}
}
