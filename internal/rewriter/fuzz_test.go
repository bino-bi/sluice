// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// FuzzRewrite pushes arbitrary SQL through the full parse → evaluate →
// rewrite pipeline. The pipeline must never panic and must never return
// an error that isn't a *pkgerr.APIError or one of the parser sentinels.
// Success requires Rewrite to return either a typed APIError or a
// non-nil result whose SQL re-parses cleanly.
func FuzzRewrite(f *testing.F) {
	seeds := []string{
		"SELECT 1",
		"SELECT id FROM pg.public.orders",
		"SELECT * FROM pg.public.customers",
		"SELECT a, b FROM t WHERE a = 1",
		"SELECT '",
		"",
		"UPDATE pg.public.orders SET total = 0",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	p := pgquery.New(parser.Options{})

	// Single-snapshot policy: allow-all + one row filter on orders and one
	// mask on customers.email. Fuzzing the SQL against a fixed policy
	// snapshot is the configuration with the most interesting code paths.
	policies := []apitypes.Object{
		&apitypes.SQLAccessPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
			Metadata: apitypes.ObjectMeta{Name: "allow-all"},
			Spec: apitypes.SQLAccessSpec{
				Effect: apitypes.EffectAllow,
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"*"}}},
				}},
			},
		},
		&apitypes.RowFilterPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindRowFilterPolicy},
			Metadata: apitypes.ObjectMeta{Name: "tenant-isolation"},
			Spec: apitypes.RowFilterSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}}},
				}},
				Filter: apitypes.FilterSpec{
					Predicate: &apitypes.Predicate{
						Column: "tenant_id", Op: apitypes.PredOpEquals, Value: "t-1",
					},
				},
			},
		},
		&apitypes.ColumnMaskPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
			Metadata: apitypes.ObjectMeta{Name: "mask-email"},
			Spec: apitypes.ColumnMaskSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}, Columns: []string{"email"}}},
				}},
				Mask: apitypes.MaskSpec{Type: apitypes.MaskConstant, Args: apitypes.MaskArgs{Value: "***"}},
			},
		},
	}
	eng := policy.New(policy.Options{})
	src := &config.Snapshot{Policies: policies, ByKind: map[apitypes.Kind][]apitypes.Object{}}
	for _, o := range policies {
		src.ByKind[o.GetKind()] = append(src.ByKind[o.GetKind()], o)
	}
	if err := eng.ApplySnapshot(context.Background(), src); err != nil {
		f.Fatalf("apply snapshot: %v", err)
	}

	rw := rewriter.New(rewriter.Options{Parser: p, DefaultCatalog: "pg"})
	user := &identity.UserCtx{Subject: "fuzz", Claims: map[string]any{"tenant_id": "t-1"}}

	f.Fuzz(func(t *testing.T, sql string) {
		ast, _ := p.Parse(context.Background(), sql)
		in := policy.Input{User: user}
		if ast != nil {
			in.AST = ast
			in.Shape = ast.Shape()
			in.Tables = ast.Tables()
		}
		dec, err := eng.Evaluate(context.Background(), in)
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		res, err := rw.Rewrite(context.Background(), rewriter.RewriteRequest{
			AST:      ast,
			Decision: dec,
			User:     user,
			Raw:      sql,
		})
		if err != nil {
			var apiErr *pkgerr.APIError
			if errors.As(err, &apiErr) {
				return
			}
			if errors.Is(err, parser.ErrSyntax) ||
				errors.Is(err, parser.ErrUnsupported) ||
				errors.Is(err, parser.ErrDeparseFailed) ||
				errors.Is(err, parser.ErrInputTooLarge) ||
				errors.Is(err, rewriter.ErrUnsupportedSyntax) ||
				errors.Is(err, rewriter.ErrDeparseFailed) ||
				errors.Is(err, rewriter.ErrForeignAST) ||
				errors.Is(err, rewriter.ErrSchemaMissing) {
				return
			}
			t.Fatalf("non-APIError / non-sentinel: %T %v", err, err)
		}
		if res == nil {
			t.Fatalf("nil result without error")
		}
		if !res.Changed {
			return
		}
		// A rewritten statement must re-parse — otherwise we've produced
		// invalid SQL, and the executor would error out at runtime.
		if _, err := p.Parse(context.Background(), res.SQL); err != nil {
			t.Fatalf("rewritten SQL does not re-parse: %q\n  err: %v", res.SQL, err)
		}
	})
}
