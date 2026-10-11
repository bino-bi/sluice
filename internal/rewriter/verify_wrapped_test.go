// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter

import (
	"context"
	"errors"
	"testing"

	pg "github.com/pganalyze/pg_query_go/v6"

	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgmask "github.com/bino-bi/sluice/pkg/mask"
)

// verifyWrapped is the backstop after the wrap pass: a protected table the
// pass missed must refuse the statement, not be read raw. The statements
// here never went through the pass, so every protected table is bare.
func TestVerifyWrapped_RefusesBareProtectedTable(t *testing.T) {
	pred, err := policy.CompilePredicateSpec(&apitypes.Predicate{Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "t-1"})
	if err != nil {
		t.Fatalf("compile predicate: %v", err)
	}
	s := &state{
		ctx: context.Background(),
		decision: &policy.Decision{
			Outcome:    policy.OutcomeAllow,
			RowFilters: map[string]*policy.CompiledFilter{"pg.public.orders": policy.NewFilter("pg.public.orders", pred, apitypes.CombineRestrictive, "f")},
			ColumnMasks: map[string]*policy.CompiledMask{
				"pg.public.customers.email": policy.NewMask("pg.public.customers", "email", apitypes.MaskNull, pkgmask.Args{}, "m"),
			},
		},
		defaultCatalog: "pg",
	}
	for sql, refused := range map[string]bool{
		"SELECT id FROM pg.public.orders":                                                    true,
		"SELECT id FROM pg.public.customers":                                                 true,
		"SELECT i.id FROM pg.public.items i WHERE i.id IN (SELECT id FROM pg.public.orders)": true,
		"WITH x AS (SELECT id FROM pg.public.customers) SELECT id FROM x":                    true,
		"SELECT id FROM pg.public.items":                                                     false,
	} {
		raw, err := pg.Parse(sql)
		if err != nil {
			t.Fatalf("parse %s: %v", sql, err)
		}
		err = s.verifyWrapped(raw)
		switch {
		case refused && !errors.Is(err, ErrUnsupportedSyntax):
			t.Errorf("%s: err = %v, want ErrUnsupportedSyntax", sql, err)
		case !refused && err != nil:
			t.Errorf("%s: %v", sql, err)
		}
	}
}
