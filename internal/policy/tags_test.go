// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func classification(name string, rules ...apitypes.ClassificationRule) *apitypes.DataClassification {
	return &apitypes.DataClassification{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionBeta1, Kind: apitypes.KindDataClassification},
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec:     apitypes.DataClassificationSpec{Rules: rules},
	}
}

func tagMaskPolicy(name string, tags []string) *apitypes.ColumnMaskPolicy {
	return &apitypes.ColumnMaskPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
		Metadata: apitypes.ObjectMeta{Name: name, Priority: 50},
		Spec: apitypes.ColumnMaskSpec{
			Match: apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tags: tags}}}},
			Mask:  apitypes.MaskSpec{Type: apitypes.MaskNull},
		},
	}
}

func snapshotWithByKind(objs ...apitypes.Object) *config.Snapshot {
	s := &config.Snapshot{Policies: objs, ByKind: map[apitypes.Kind][]apitypes.Object{}}
	for _, o := range objs {
		s.ByKind[o.GetKind()] = append(s.ByKind[o.GetKind()], o)
	}
	return s
}

func TestTags_ColumnMaskViaClassification(t *testing.T) {
	dc := classification("pii",
		apitypes.ClassificationRule{
			Resources: apitypes.ResourceSelector{Schemas: []string{"crm"}, Tables: []string{"customers"}, Columns: []string{"email", "phone"}},
			Tags:      []string{"pii.contact"},
		})
	mask := tagMaskPolicy("mask-pii", []string{"pii.contact"})

	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snapshotWithByKind(allowAll(0), dc, mask)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "crm", Table: "customers"}},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// The tag expands to the email + phone column masks.
	for _, col := range []string{"email", "phone"} {
		key := "pg.crm.customers." + col
		if _, ok := dec.ColumnMasks[key]; !ok {
			t.Errorf("expected tag-driven mask on %s; masks=%v", key, maskKeysList(dec))
		}
	}
}

func TestTags_TableOutsideClassificationNotMasked(t *testing.T) {
	dc := classification("pii", apitypes.ClassificationRule{
		Resources: apitypes.ResourceSelector{Schemas: []string{"crm"}, Tables: []string{"customers"}, Columns: []string{"email"}},
		Tags:      []string{"pii.contact"},
	})
	mask := tagMaskPolicy("mask-pii", []string{"pii.contact"})

	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snapshotWithByKind(allowAll(0), dc, mask)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// A table not covered by the classification must not be masked.
	dec, _ := eng.Evaluate(context.Background(), Input{
		User:   &identity.UserCtx{Subject: "u"},
		Tables: []parser.TableRef{{Catalog: "pg", Schema: "public", Table: "orders"}},
	})
	if len(dec.ColumnMasks) != 0 {
		t.Errorf("mask applied to a table outside the classification: %v", maskKeysList(dec))
	}
}

func TestTags_UnknownTagFailsCompile(t *testing.T) {
	mask := tagMaskPolicy("mask-typo", []string{"pii.contct"}) // typo, no classification defines it
	_, err := Compile(context.Background(), snapshotWithByKind(allowAll(0), mask))
	if err == nil || !strings.Contains(err.Error(), "unknown tag") {
		t.Fatalf("expected unknown-tag compile error, got %v", err)
	}
}

func maskKeysList(d *Decision) []string {
	out := make([]string, 0, len(d.ColumnMasks))
	for k := range d.ColumnMasks {
		out = append(out, k)
	}
	return out
}

// A selector that names only tags must match the tagged tables and no
// other. An allow by tag must not cover an untagged table, and a deny by
// tag must not block an untagged one.
func TestTags_TagOnlySelectorMatchesOnlyTaggedTables(t *testing.T) {
	dc := classification("public-data", apitypes.ClassificationRule{
		Resources: apitypes.ResourceSelector{Catalogs: []string{"shop"}, Schemas: []string{"main"}, Tables: []string{"orders"}},
		Tags:      []string{"public"},
	})
	byTag := func(name string, effect apitypes.Effect, tag string) *apitypes.SQLAccessPolicy {
		return &apitypes.SQLAccessPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
			Metadata: apitypes.ObjectMeta{Name: name, Priority: 100},
			Spec: apitypes.SQLAccessSpec{Effect: effect, Match: apitypes.Selector{Any: []apitypes.Clause{
				{Resources: &apitypes.ResourceSelector{Tags: []string{tag}}},
			}}},
		}
	}
	orders := parser.TableRef{Catalog: "shop", Schema: "main", Table: "orders"}
	admin := parser.TableRef{Catalog: "shop", Schema: "main", Table: "admin_users"}

	eng := New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snapshotWithByKind(dc, byTag("allow-public", apitypes.EffectAllow, "public"))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, tc := range []struct {
		tables []parser.TableRef
		want   Outcome
	}{
		{[]parser.TableRef{orders}, OutcomeAllow},
		{[]parser.TableRef{admin}, OutcomeDeny},
		{[]parser.TableRef{orders, admin}, OutcomeDeny},
	} {
		dec, err := eng.Evaluate(context.Background(), Input{Tables: tc.tables})
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		if dec.Outcome != tc.want {
			t.Errorf("allow by tag, tables %v: outcome = %s, want %s", tc.tables, dec.Outcome, tc.want)
		}
	}

	eng = New(Options{})
	if err := eng.ApplySnapshot(context.Background(), snapshotWithByKind(dc, allowAll(0), byTag("deny-public", apitypes.EffectDeny, "public"))); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dec, err := eng.Evaluate(context.Background(), Input{Tables: []parser.TableRef{admin}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if dec.Outcome != OutcomeAllow {
		t.Errorf("deny by tag on an untagged table: outcome = %s (%+v), want allow", dec.Outcome, dec.DenyReason)
	}
}

// A classification rule that names only columns says nothing about which
// tables it covers, so it would make an allow by its tag cover every table.
// Such an allow is refused; masks and denies by the tag still load.
func TestTags_AllowByColumnOnlyTagIsRefused(t *testing.T) {
	dc := classification("contact", apitypes.ClassificationRule{
		Resources: apitypes.ResourceSelector{Columns: []string{"email"}},
		Tags:      []string{"contact"},
	})
	allow := &apitypes.SQLAccessPolicy{
		TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindSQLAccessPolicy},
		Metadata: apitypes.ObjectMeta{Name: "allow-contact", Priority: 100},
		Spec: apitypes.SQLAccessSpec{Effect: apitypes.EffectAllow, Match: apitypes.Selector{Any: []apitypes.Clause{
			{Resources: &apitypes.ResourceSelector{Tags: []string{"contact"}}},
		}}},
	}
	err := New(Options{}).ApplySnapshot(context.Background(), snapshotWithByKind(dc, allow))
	if err == nil || !strings.Contains(err.Error(), "names no table") {
		t.Fatalf("err = %v, want the allow refused", err)
	}
	if err := New(Options{}).ApplySnapshot(context.Background(), snapshotWithByKind(dc, allowAll(0), tagMaskPolicy("mask-contact", []string{"contact"}))); err != nil {
		t.Fatalf("a mask by a column-only tag must load: %v", err)
	}
}
