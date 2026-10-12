// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// EXPLAIN prints the rewritten plan: mask expressions with their salt as a
// constant, and statistics over the rows a row filter hides. It is refused
// when a filter or mask applies.
func TestRewrite_ExplainRefusedOnProtectedTables(t *testing.T) {
	rw := rewriter.New(rewriter.Options{
		Parser:         pgquery.New(parser.Options{}),
		DefaultCatalog: "pg",
		Salts:          goldenSaltStore{},
	})
	user := &identity.UserCtx{Subject: "u", Claims: map[string]any{"tenant_id": "t-1"}}
	saltedHash := maskPolicy("hash-email", "employees", []string{"email"}, apitypes.MaskSpec{
		Type: apitypes.MaskHash,
		Args: apitypes.MaskArgs{Algorithm: apitypes.HashSHA256, SaltRef: "secret://env/MASK_SALT"},
	})

	refused := []struct {
		sql    string
		policy apitypes.Object
	}{
		{"EXPLAIN SELECT id FROM pg.hr.employees WHERE email = 'x'", saltedHash},
		{"EXPLAIN ANALYZE SELECT email FROM pg.hr.employees", saltedHash},
		{"EXPLAIN SELECT count(*) FROM pg.public.orders", tenantFilter("orders")},
		{"EXPLAIN ANALYZE SELECT id FROM pg.public.orders WHERE total > 100", tenantFilter("orders")},
	}
	for _, tc := range refused {
		dec, ast := evaluate(t, tc.sql, user, allowAll(0), tc.policy)
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: tc.sql})
		if err == nil {
			t.Errorf("%s: served %q, want ERR_UNSUPPORTED_SYNTAX", tc.sql, res.SQL)
			continue
		}
		if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax {
			t.Errorf("%s: code = %s, want ERR_UNSUPPORTED_SYNTAX", tc.sql, ae.Code)
		}
	}

	sql := "EXPLAIN SELECT id FROM pg.public.orders"
	dec, ast := evaluate(t, sql, user, allowAll(0), capRows(10))
	if _, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: sql}); err != nil {
		t.Errorf("%s with only a limit: %v", sql, err)
	}
}
