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

// The rewriter binds filter values and mask params as $1, $2, ... A $n in
// the caller's text would read them, so it is refused on every path.
func TestRewrite_CallerParamRefused(t *testing.T) {
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
	for _, tc := range []struct {
		name     string
		sql      string
		policies []apitypes.Object
	}{
		{"salted hash", "SELECT $1 AS s, email FROM pg.hr.employees", []apitypes.Object{allowAll(0), saltedHash}},
		{"row filter", "SELECT $1 AS p, id FROM pg.public.orders", []apitypes.Object{allowAll(0), tenantFilter("orders")}},
		{"nested select", "SELECT id FROM pg.public.orders WHERE id IN (SELECT $1::int)", []apitypes.Object{allowAll(0), tenantFilter("orders")}},
		{"pass-through", "SELECT id FROM pg.public.orders WHERE id = $1", []apitypes.Object{allowAll(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec, ast := evaluate(t, tc.sql, user, tc.policies...)
			res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{AST: ast, Decision: dec, User: user, Raw: tc.sql})
			if err == nil {
				t.Fatalf("served %q with %v, want ERR_UNSUPPORTED_SYNTAX", res.SQL, res.Params)
			}
			if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodeUnsupportedSyntax || ae.Message != parser.ErrParamRef.Error() {
				t.Errorf("err = %v, want ERR_UNSUPPORTED_SYNTAX %q", err, parser.ErrParamRef)
			}
		})
	}
}
