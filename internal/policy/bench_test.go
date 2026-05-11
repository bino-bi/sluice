// SPDX-License-Identifier: AGPL-3.0-or-later

package policy_test

import (
	"context"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// BenchmarkEvaluate measures a realistic evaluation: allow-all + one row
// filter + two column masks over a single-table SELECT. Anchors the
// policy hot path for regression runs.
func BenchmarkEvaluate(b *testing.B) {
	p := pgquery.New(parser.Options{})
	ctx := context.Background()
	ast, err := p.Parse(ctx, "SELECT id, email, ssn FROM pg.public.customers WHERE active = true")
	if err != nil {
		b.Fatalf("parse: %v", err)
	}

	pols := []apitypes.Object{
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
			Metadata: apitypes.ObjectMeta{Name: "tenant"},
			Spec: apitypes.RowFilterSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}}},
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
		&apitypes.ColumnMaskPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
			Metadata: apitypes.ObjectMeta{Name: "mask-ssn"},
			Spec: apitypes.ColumnMaskSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{
					{Resources: &apitypes.ResourceSelector{Tables: []string{"customers"}, Columns: []string{"ssn"}}},
				}},
				Mask: apitypes.MaskSpec{Type: apitypes.MaskNull},
			},
		},
	}
	eng := policy.New(policy.Options{})
	src := &config.Snapshot{Policies: pols, ByKind: map[apitypes.Kind][]apitypes.Object{}}
	for _, o := range pols {
		src.ByKind[o.GetKind()] = append(src.ByKind[o.GetKind()], o)
	}
	if err := eng.ApplySnapshot(ctx, src); err != nil {
		b.Fatalf("apply snapshot: %v", err)
	}

	user := &identity.UserCtx{Subject: "bench"}
	in := policy.Input{
		User:   user,
		AST:    ast,
		Shape:  ast.Shape(),
		Tables: ast.Tables(),
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := eng.Evaluate(ctx, in); err != nil {
			b.Fatalf("evaluate: %v", err)
		}
	}
}
