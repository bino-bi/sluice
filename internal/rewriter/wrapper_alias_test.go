// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
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
// the wrapper's name. Others break the statement. Such a table needs an
// alias.
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

// The wrapper of a table without an alias is named after the table alone,
// so DuckDB cannot resolve a column qualified with its schema or catalog.
func TestRewrite_WrapperQualifiedColumnRefused(t *testing.T) {
	rw := buildRewriter(t)
	user := &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}
	filter := tenantFilter("orders")
	for _, sql := range []string{
		`SELECT public.orders.id FROM pg.public.orders`,
		`SELECT pg.public.orders.id FROM pg.public.orders`,
		`SELECT "PUBLIC"."Orders".id FROM pg.public.orders`,
		`SELECT id FROM pg.public.orders WHERE public.orders.total > 1`,
		`SELECT public.orders.note.x FROM pg.public.orders`,
		`SELECT (SELECT max(public.orders.id) FROM pg.public.orders) FROM pg.public.items i`,
		// DuckDB also reads catalog.table.column.
		`SELECT pg.orders.id FROM pg.public.orders`,
	} {
		dec, ast := evaluate(t, sql, user, allowAll(0), filter)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql})
		if err == nil {
			t.Errorf("%s: served %q, want ERR_UNSUPPORTED_SYNTAX", sql, res.SQL)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax || !strings.Contains(ae.Message, `"orders"`) {
			t.Errorf("%s: err = %v, want ERR_UNSUPPORTED_SYNTAX naming the table", sql, err)
		}
	}
	for _, sql := range []string{
		`SELECT orders.id FROM pg.public.orders`,
		`SELECT o.id FROM pg.public.orders o`,
		`SELECT public.orders.id FROM pg.public.orders AS orders`,
		`SELECT orders.note.x FROM pg.public.orders`,
		`SELECT i.orders.id FROM pg.public.items i`,
		// A struct column named like the filtered table.
		`SELECT i.orders.id FROM pg.public.items i JOIN pg.public.orders ON orders.id = i.id`,
		`SELECT orders.orders.x FROM pg.public.orders`,
		`SELECT x.public.orders.id FROM pg.public.items x, pg.public.orders`,
	} {
		dec, ast := evaluate(t, sql, user, allowAll(0), filter)
		if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql}); err != nil {
			t.Errorf("%s: %v", sql, err)
		}
	}
}

// A table written without a catalog is in DuckDB's default database, so
// catalog.t.col names it when catalog is that database. Other three-part
// forms, such as alias.struct_col.field, are not refused.
func TestRewrite_WrapperDefaultDatabaseRefused(t *testing.T) {
	rw := rewriter.New(rewriter.Options{Parser: pgquery.New(parser.Options{}), DefaultDatabase: "memory"})
	user := &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}
	filter := tenantFilter("orders")
	for sql, refused := range map[string]bool{
		`SELECT memory.orders.id FROM orders`:                  true,
		`SELECT "MEMORY".orders.id FROM main.orders`:           true,
		`SELECT memory.main.orders.id FROM orders`:             true,
		`SELECT id FROM orders WHERE memory.orders.id > 1`:     true,
		`SELECT i.orders.id FROM items i, orders`:              false,
		`SELECT other.main.orders.id FROM items other, orders`: false,
		`SELECT orders.id FROM memory.main.orders`:             false,
	} {
		dec, ast := evaluate(t, sql, user, allowAll(0), filter)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql})
		switch {
		case refused && err == nil:
			t.Errorf("%s: served %q, want ERR_UNSUPPORTED_SYNTAX", sql, res.SQL)
		case refused && pkgerr.FromError(err).Code != pkgerr.CodeUnsupportedSyntax:
			t.Errorf("%s: err = %v, want ERR_UNSUPPORTED_SYNTAX", sql, err)
		case !refused && err != nil:
			t.Errorf("%s: %v", sql, err)
		}
	}
}
