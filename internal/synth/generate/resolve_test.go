// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func ptr[T any](v T) *T { return &v }

func TestResolveOrder(t *testing.T) {
	spec := &apitypes.SyntheticSpec{
		Rules: []apitypes.SyntheticRule{
			{Match: apitypes.SyntheticMatch{Tags: []string{"pii"}}, Generator: apitypes.GenRedact},
			{Match: apitypes.SyntheticMatch{Types: []string{"decimal*"}}, Generator: apitypes.GenHistogram},
		},
	}
	table := &apitypes.SyntheticTable{
		Name: "crm.customers",
		Columns: map[string]apitypes.SyntheticColumn{
			"ID":    {Generator: apitypes.GenSequence, Privacy: &apitypes.SyntheticPrivacy{MinGroupSize: ptr(10)}},
			"ghost": {Generator: apitypes.GenNull},
		},
		Rules: []apitypes.SyntheticRule{
			{Match: apitypes.SyntheticMatch{Columns: []string{"*_CODE"}, Types: []string{"VARCHAR"}}, Generator: apitypes.GenPattern},
			{Match: apitypes.SyntheticMatch{Columns: []string{"email"}}, Generator: apitypes.GenFake, Args: map[string]any{"kind": "email"}},
		},
	}
	r, err := NewResolver(spec, table)
	if err != nil {
		t.Fatal(err)
	}
	counts := profile.Counts{Rows: 100, NonNull: 100, Distinct: 100}
	for _, tc := range []struct {
		name, typ string
		tags      []string
		gen       apitypes.SyntheticGenerator
		by        string
	}{
		{"id", "BIGINT", nil, apitypes.GenSequence, "column"},
		{"Zip_Code", "VARCHAR", nil, apitypes.GenPattern, "table-rule[0]"},
		{"zip_code", "INTEGER", nil, apitypes.GenDistribution, "default"},
		// The table rule wins over the source tag rule.
		{"email", "VARCHAR", []string{"pii"}, apitypes.GenFake, "table-rule[1]"},
		{"phone", "VARCHAR", []string{"pii"}, apitypes.GenRedact, "source-rule[0]"},
		{"revenue", "DECIMAL(12,2)", nil, apitypes.GenHistogram, "source-rule[1]"},
	} {
		c, err := r.Resolve(Column{Name: tc.name, Type: tc.typ}, counts, tc.tags)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Generator != tc.gen || c.ChosenBy != tc.by {
			t.Errorf("%s: got %s by %s, want %s by %s", tc.name, c.Generator, c.ChosenBy, tc.gen, tc.by)
		}
	}
	c, _ := r.Resolve(Column{Name: "id", Type: "BIGINT"}, counts, nil)
	if c.Privacy == nil || *c.Privacy.MinGroupSize != 10 {
		t.Errorf("column privacy not passed on: %+v", c.Privacy)
	}
	if got := r.MissingColumns([]string{"id", "email"}); !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Errorf("MissingColumns = %v", got)
	}
}

func TestResolveDefaults(t *testing.T) {
	r, err := NewResolver(&apitypes.SyntheticSpec{}, &apitypes.SyntheticTable{})
	if err != nil {
		t.Fatal(err)
	}
	many := profile.Counts{Rows: 1000, NonNull: 1000, Distinct: 500}
	few := profile.Counts{Rows: 1000, NonNull: 1000, Distinct: 4}
	// chain lists the Fallback generators in order.
	for _, tc := range []struct {
		typ     string
		counts  profile.Counts
		gen     apitypes.SyntheticGenerator
		chain   string
		warning string
	}{
		{"INTEGER", many, apitypes.GenDistribution, "null", ""},
		{"DECIMAL(12,2)", many, apitypes.GenDistribution, "null", ""},
		{"BOOLEAN", few, apitypes.GenBoolean, "null", ""},
		{"VARCHAR", few, apitypes.GenCategorical, "randomString null", ""},
		{"VARCHAR", many, apitypes.GenRandomString, "null", ""},
		{"ENUM('M', 'W')", few, apitypes.GenCategorical, "null", ""},
		{"DATE", many, apitypes.GenDateDistribution, "null", ""},
		{"TIMESTAMP WITH TIME ZONE", many, apitypes.GenDateDistribution, "null", ""},
		{"TIME", many, apitypes.GenDistribution, "null", ""},
		{"UUID", many, apitypes.GenUUID, "", ""},
		{"BLOB", many, apitypes.GenNull, "", "type BLOB, written as NULL"},
		{"INTEGER", profile.Counts{Rows: 100, NonNull: 4}, apitypes.GenNull, "", "fewer than minGroupSize non-null rows, written as NULL"},
	} {
		c, err := r.Resolve(Column{Name: "c", Type: tc.typ}, tc.counts, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if c.Generator != tc.gen || c.ChosenBy != "default" || c.Warning != tc.warning {
			t.Errorf("%s: got %s (%q)", tc.typ, c.Generator, c.Warning)
		}
		var chain []string
		last := c
		for fb := c.Fallback; fb != nil; fb = fb.Fallback {
			chain = append(chain, string(fb.Generator))
			if fb.Generator == apitypes.GenRandomString && fb.Args["length"] != "source" {
				t.Errorf("%s: fallback args %v", tc.typ, fb.Args)
			}
			last = *fb
		}
		if got := strings.Join(chain, " "); got != tc.chain {
			t.Errorf("%s: fallback chain %q, want %q", tc.typ, got, tc.chain)
		}
		if last.Generator == apitypes.GenNull && last.Warning == "" {
			t.Errorf("%s: chain ends in null without a warning", tc.typ)
		}
	}
}

func TestResolveStrictTagsAndTypes(t *testing.T) {
	counts := profile.Counts{Rows: 100, NonNull: 100, Distinct: 100}
	r, _ := NewResolver(&apitypes.SyntheticSpec{}, &apitypes.SyntheticTable{})
	_, err := r.Resolve(Column{Name: "email", Type: "VARCHAR"}, counts, []string{"contact", "pii"})
	if err == nil || !strings.Contains(err.Error(), "column email: tag pii") {
		t.Errorf("default strictTags: err = %v", err)
	}
	if _, err := r.Resolve(Column{Name: "note", Type: "VARCHAR"}, counts, []string{"secret"}); err != nil {
		t.Errorf("tag not strict: %v", err)
	}

	custom := &apitypes.SyntheticSpec{Privacy: &apitypes.SyntheticPrivacy{StrictTags: []string{"secret"}}}
	r, _ = NewResolver(custom, &apitypes.SyntheticTable{})
	if _, err := r.Resolve(Column{Name: "note", Type: "VARCHAR"}, counts, []string{"secret"}); err == nil {
		t.Error("custom strictTags not applied")
	}
	if _, err := r.Resolve(Column{Name: "email", Type: "VARCHAR"}, counts, []string{"pii"}); err != nil {
		t.Errorf("custom strictTags replace the default: %v", err)
	}

	typed := &apitypes.SyntheticTable{Columns: map[string]apitypes.SyntheticColumn{"n": {Generator: apitypes.GenRandomString}}}
	r, _ = NewResolver(&apitypes.SyntheticSpec{}, typed)
	if _, err := r.Resolve(Column{Name: "n", Type: "INTEGER"}, counts, nil); err == nil || !strings.Contains(err.Error(), "column n: generator does not fit") {
		t.Errorf("type check: err = %v", err)
	}
}
