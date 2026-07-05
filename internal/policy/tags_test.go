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
