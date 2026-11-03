// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// synDoc returns a synthetic DataSource whose first table has one column
// "c" with the given generator line and args block (YAML, indented for
// the column).
func synDoc(name, column string) string {
	return `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: ` + name + `
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
        columns:
          c: ` + column + `
`
}

func decodeSyn(t *testing.T, doc string) (*DataSource, error) {
	t.Helper()
	objs, err := Decode(strings.NewReader(doc), WithStrictUnknown(true))
	if err != nil {
		return nil, err
	}
	return objs[0].(*DataSource), nil
}

func TestSyntheticValidArgs(t *testing.T) {
	t.Parallel()
	cols := map[string]string{
		"distribution":     `{ generator: distribution, args: { decimals: 2, integer: false, smooth: true, nullRate: source, unique: true } }`,
		"uniform":          `{ generator: uniform, args: { min: 1, max: 5.5 } }`,
		"normal":           `{ generator: normal, args: { truncate: false, decimals: 0, integer: true } }`,
		"lognormal":        `{ generator: lognormal, args: { truncate: true } }`,
		"histogram":        `{ generator: histogram, args: { bins: 20 } }`,
		"bucket edges":     `{ generator: bucket, args: { edges: [0, 10, 20.5] } }`,
		"bucket n":         `{ generator: bucket, args: { buckets: 5 } }`,
		"constant":         `{ generator: constant, args: { value: "x", nullRate: 0.1 } }`,
		"sequence":         `{ generator: sequence, args: { start: 100, step: -2 } }`,
		"range":            `{ generator: range, args: { min: 0, max: 10, decimals: 1 } }`,
		"categorical":      `{ generator: categorical, args: { otherLabel: other, maxValues: 10 } }`,
		"uniformCategory":  `{ generator: uniformCategory }`,
		"weighted":         `{ generator: weighted, args: { values: { a: 1, "Y": 2.5 } } }`,
		"boolean":          `{ generator: boolean, args: { trueRate: 0.3 } }`,
		"shuffleLabels":    `{ generator: shuffleLabels, args: { maxValues: 50 } }`,
		"randomString":     `{ generator: randomString, args: { length: { min: 3, max: 9 }, initial: "Q", charset: alnum, case: title } }`,
		"pattern":          `{ generator: pattern, args: { maxPatterns: 5 } }`,
		"regex":            `{ generator: regex, args: { pattern: "[A-Z]{2}-[0-9]{4}" } }`,
		"fake":             `{ generator: fake, args: { kind: city, locale: en, keepInitial: true } }`,
		"uuid":             `{ generator: uuid, args: { version: 7 } }`,
		"prefixKeep":       `{ generator: prefixKeep, args: { prefixLength: 3 } }`,
		"loremText":        `{ generator: loremText, args: { words: 12, vocabulary: de } }`,
		"randomText":       `{ generator: randomText, args: { length: source, vocabulary: lorem } }`,
		"redact":           `{ generator: redact, args: { value: "***" } }`,
		"dateDistribution": `{ generator: dateDistribution, args: { granularity: month, keepWeekday: true, keepSeasonality: true } }`,
		"dateRange":        `{ generator: dateRange, args: { min: "2020-01-01", max: "2024-06-30T12:00:00Z", granularity: day } }`,
		"null":             `{ generator: "null" }`,
		"template":         `{ generator: template, args: { template: "{First_Name}.{last_name}@example.org {{x}}", lower: true, nullRate: 0.1 } }`,
	}
	for name, col := range cols {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeSyn(t, synDoc("crmsynth", col)); err != nil {
				t.Fatalf("decode: %v", err)
			}
		})
	}
	// Every generator constant has a test case above.
	for g := range syntheticGenerators {
		found := false
		for _, col := range cols {
			if strings.Contains(col, "generator: "+string(g)+" ") || strings.Contains(col, "generator: "+string(g)+",") || strings.Contains(col, `generator: "`+string(g)+`"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("generator %s has no valid-args case", g)
		}
	}
}

func TestSyntheticFullSpec(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/CRM_SYNTH_SEED
    build:
      onStart: ifMissing
    privacy:
      minGroupSize: 5
      clipOutliers: { lower: 0.005, upper: 0.995 }
      extremeJitter: off
      spikeWarning: true
      strictTags: [pii]
    rules:
      - match: { tags: [pii] }
        generator: randomString
        args: { length: source }
    tables:
      - name: public.customers
        size: { rows: 10000 }
        privacy: { minGroupSize: 10, extremeJitter: { min: 0.01, max: 0.02 } }
        columns:
          id: { generator: sequence }
          revenue: { generator: distribution, args: { decimals: 2 }, privacy: { clipOutliers: { lower: 0.01, upper: 0.99 } } }
        rules:
          - match: { columns: ["*_code"], types: [VARCHAR] }
            generator: pattern
`
	ds, err := decodeSyn(t, doc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	syn := ds.Spec.Synthetic
	if syn.Build.OnStart != BuildIfMissing {
		t.Errorf("onStart = %q", syn.Build.OnStart)
	}
	if j := syn.Privacy.ExtremeJitter; j == nil || !j.Off {
		t.Errorf("extremeJitter: off did not load as off: %+v", j)
	}
	if j := syn.Tables[0].Privacy.ExtremeJitter; j == nil || j.Off || j.Min != 0.01 || j.Max != 0.02 {
		t.Errorf("table extremeJitter = %+v", j)
	}
	if r := syn.Tables[0].Size.Rows; r == nil || *r != 10000 {
		t.Errorf("rows = %v", r)
	}
	if got := syn.Tables[0].Columns["revenue"].Args["decimals"]; got != float64(2) {
		t.Errorf("decimals = %#v", got)
	}
}

func TestExtremeJitterJSON(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`false`, `"off"`} {
		var j ExtremeJitter
		if err := json.Unmarshal([]byte(in), &j); err != nil || !j.Off {
			t.Errorf("%s: got %+v, %v", in, j, err)
		}
		out, _ := json.Marshal(j)
		if string(out) != `"off"` {
			t.Errorf("%s: marshal = %s", in, out)
		}
	}
	var j ExtremeJitter
	if err := json.Unmarshal([]byte(`{"min":0.1,"max":0.2}`), &j); err != nil || j.Off || j.Min != 0.1 || j.Max != 0.2 {
		t.Errorf("object: got %+v, %v", j, err)
	}
	if out, _ := json.Marshal(j); string(out) != `{"min":0.1,"max":0.2}` {
		t.Errorf("marshal = %s", out)
	}
	for _, in := range []string{`true`, `"on"`, `{"min":0.1,"mx":0.2}`, `3`} {
		if err := json.Unmarshal([]byte(in), &j); err == nil {
			t.Errorf("%s: want error", in)
		}
	}
}

func TestSyntheticInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, doc, field string
	}{
		{"unknown generator", synDoc("crmsynth", `{ generator: magic }`), "spec.synthetic.tables[0].columns.c.generator"},
		{"no generator", synDoc("crmsynth", `{ args: { decimals: 1 } }`), "spec.synthetic.tables[0].columns.c.generator"},
		{"unknown arg", synDoc("crmsynth", `{ generator: distribution, args: { decimal: 2 } }`), "spec.synthetic.tables[0].columns.c.args.decimal"},
		{"wrong arg type", synDoc("crmsynth", `{ generator: distribution, args: { decimals: "two" } }`), "spec.synthetic.tables[0].columns.c.args.decimals"},
		{"arg out of range", synDoc("crmsynth", `{ generator: histogram, args: { bins: 1 } }`), "spec.synthetic.tables[0].columns.c.args.bins"},
		{"not an integer", synDoc("crmsynth", `{ generator: sequence, args: { start: 1.5 } }`), "spec.synthetic.tables[0].columns.c.args.start"},
		{"step 0", synDoc("crmsynth", `{ generator: sequence, args: { step: 0 } }`), "spec.synthetic.tables[0].columns.c.args.step"},
		{"missing required", synDoc("crmsynth", `{ generator: range, args: { min: 1 } }`), "spec.synthetic.tables[0].columns.c.args.max"},
		{"range min above max", synDoc("crmsynth", `{ generator: range, args: { min: 5, max: 1 } }`), "spec.synthetic.tables[0].columns.c.args.max"},
		{"bucket both", synDoc("crmsynth", `{ generator: bucket, args: { edges: [1, 2], buckets: 3 } }`), "spec.synthetic.tables[0].columns.c.args.buckets"},
		{"edges unsorted", synDoc("crmsynth", `{ generator: bucket, args: { edges: [2, 1] } }`), "spec.synthetic.tables[0].columns.c.args.edges"},
		{"weight 0", synDoc("crmsynth", `{ generator: weighted, args: { values: { a: 0 } } }`), "spec.synthetic.tables[0].columns.c.args.values"},
		{"bad nullRate", synDoc("crmsynth", `{ generator: "null", args: { nullRate: 2 } }`), "spec.synthetic.tables[0].columns.c.args.nullRate"},
		{"constant list", synDoc("crmsynth", `{ generator: constant, args: { value: [1] } }`), "spec.synthetic.tables[0].columns.c.args.value"},
		{"length min above max", synDoc("crmsynth", `{ generator: randomString, args: { length: { min: 9, max: 3 } } }`), "spec.synthetic.tables[0].columns.c.args.length"},
		{"initial two chars", synDoc("crmsynth", `{ generator: randomString, args: { initial: "ab" } }`), "spec.synthetic.tables[0].columns.c.args.initial"},
		{"fake locale", synDoc("crmsynth", `{ generator: fake, args: { kind: name, locale: de } }`), "spec.synthetic.tables[0].columns.c.args.locale"},
		{"fake keepInitial", synDoc("crmsynth", `{ generator: fake, args: { kind: email, keepInitial: true } }`), "spec.synthetic.tables[0].columns.c.args.keepInitial"},
		{"uuid version", synDoc("crmsynth", `{ generator: uuid, args: { version: 5 } }`), "spec.synthetic.tables[0].columns.c.args.version"},
		{"bad date", synDoc("crmsynth", `{ generator: dateRange, args: { min: "yesterday" } }`), "spec.synthetic.tables[0].columns.c.args.min"},
		{"regex huge", synDoc("crmsynth", `{ generator: regex, args: { pattern: "(a{1000}){1000}" } }`), "spec.synthetic.tables[0].columns.c.args.pattern"},
		{"regex long", synDoc("crmsynth", `{ generator: regex, args: { pattern: "[a-z]{600}[0-9]{600}" } }`), "spec.synthetic.tables[0].columns.c.args.pattern"},
		{"regex open repeat", synDoc("crmsynth", `{ generator: regex, args: { pattern: "(x{995,})" } }`), "spec.synthetic.tables[0].columns.c.args.pattern"},
		{"regex empty width", synDoc("crmsynth", `{ generator: regex, args: { pattern: "^$\\b" } }`), "spec.synthetic.tables[0].columns.c.args.pattern"},
		{"regex syntax", synDoc("crmsynth", `{ generator: regex, args: { pattern: "(a" } }`), "spec.synthetic.tables[0].columns.c.args.pattern"},
		{"template unclosed", synDoc("crmsynth", `{ generator: template, args: { template: "{a" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template empty placeholder", synDoc("crmsynth", `{ generator: template, args: { template: "x{}" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template no placeholder", synDoc("crmsynth", `{ generator: template, args: { template: "plain {{text}}" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template stray close", synDoc("crmsynth", `{ generator: template, args: { template: "{a} }" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template not a string", synDoc("crmsynth", `{ generator: template, args: { template: 3 } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template too long", synDoc("crmsynth", `{ generator: template, args: { template: "{a}`+strings.Repeat("x", 1024)+`" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template missing", synDoc("crmsynth", `{ generator: template }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"template unique", synDoc("crmsynth", `{ generator: template, args: { template: "{a}", unique: true } }`), "spec.synthetic.tables[0].columns.c.args.unique"},
		{"template self ref", synDoc("crmsynth", `{ generator: template, args: { template: "{a}-{C}" } }`), "spec.synthetic.tables[0].columns.c.args.template"},
		{"column strictTags", synDoc("crmsynth", `{ generator: "null", privacy: { strictTags: [pii] } }`), "spec.synthetic.tables[0].columns.c.privacy.strictTags"},
		{"dash in name", synDoc("crm-synth", `{ generator: "null" }`), "metadata.name"},
		{"leading digit", synDoc("1crm", `{ generator: "null" }`), "metadata.name"},
		{"literal seed", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "secret://env/SEED", "my-seed", 1), "spec.synthetic.seed"},
		{"no seed", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "    seed: secret://env/SEED\n", "", 1), "spec.synthetic.seed"},
		{"no source", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "    source: crm\n", "", 1), "spec.synthetic.source"},
		{"ratio above 1", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "ratio: 0.2", "ratio: 1.5", 1), "spec.synthetic.tables[0].size.ratio"},
		{"ratio and rows", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "ratio: 0.2", "ratio: 0.2, rows: 5", 1), "spec.synthetic.tables[0].size"},
		{"negative rows", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "ratio: 0.2", "rows: -1", 1), "spec.synthetic.tables[0].size.rows"},
		{"bad table name", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "crm.customers", "customers", 1), "spec.synthetic.tables[0].name"},
		{"path on synthetic", strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "  type: synthetic\n", "  type: synthetic\n  path: /x.db\n", 1), "spec.path"},
		{"no synthetic block", "apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: crmsynth\nspec:\n  type: synthetic\n", "spec.synthetic"},
		{"synthetic on other type", "apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: crm\nspec:\n  type: sqlite\n  path: /x.db\n  synthetic:\n    source: x\n", "spec.synthetic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeSyn(t, tc.doc)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %v", err)
			}
			if ve.Field != tc.field {
				t.Fatalf("field = %q (%s), want %q", ve.Field, ve.Reason, tc.field)
			}
		})
	}
}

func TestSyntheticInvalidSpecLevel(t *testing.T) {
	t.Parallel()
	base := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
`
	table := "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n"
	cases := []struct {
		name, body, field string
	}{
		{"no tables", "", "spec.synthetic.tables"},
		{"duplicate table", table + "      - name: CRM.customers\n        size: { ratio: 0.1 }\n", "spec.synthetic.tables[1].name"},
		{"duplicate column", table + "        columns:\n          Email: { generator: \"null\" }\n          email: { generator: \"null\" }\n", "spec.synthetic.tables[0].columns.email"},
		{"table strictTags", "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { strictTags: [pii] }\n", "spec.synthetic.tables[0].privacy.strictTags"},
		{"minGroupSize 1", "    privacy: { minGroupSize: 1 }\n" + table, "spec.synthetic.privacy.minGroupSize"},
		{"clip reversed", "    privacy: { clipOutliers: { lower: 0.9, upper: 0.1 } }\n" + table, "spec.synthetic.privacy.clipOutliers"},
		{"jitter too big", "    privacy: { extremeJitter: { min: 0.1, max: 0.6 } }\n" + table, "spec.synthetic.privacy.extremeJitter"},
		{"bad tag", "    privacy: { strictTags: [PII] }\n" + table, "spec.synthetic.privacy.strictTags"},
		{"onStart", "    build: { onStart: always }\n" + table, "spec.synthetic.build.onStart"},
		{"empty match", "    rules:\n      - match: {}\n        generator: redact\n" + table, "spec.synthetic.rules[0].match"},
		{"bad glob", "    rules:\n      - match: { columns: [\"a\\\\\"] }\n        generator: redact\n" + table, "spec.synthetic.rules[0].match.columns"},
		{"rule arg", "    rules:\n      - match: { tags: [pii] }\n        generator: redact\n        args: { length: 3 }\n" + table, "spec.synthetic.rules[0].args.length"},
		{"table rule generator", table + "        rules:\n          - match: { types: [VARCHAR] }\n            generator: nope\n", "spec.synthetic.tables[0].rules[0].generator"},
		{"template in source rule", "    rules:\n      - match: { columns: [email] }\n        generator: template\n        args: { template: \"{a}\" }\n" + table, "spec.synthetic.rules[0].generator"},
		{"template in table rule", table + "        rules:\n          - match: { columns: [email] }\n            generator: template\n            args: { template: \"{a}\" }\n", "spec.synthetic.tables[0].rules[0].generator"},
		{"template cycle", table + "        columns:\n          a: { generator: template, args: { template: \"{b}\" } }\n          b: { generator: template, args: { template: \"{A}\" } }\n", "spec.synthetic.tables[0].columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeSyn(t, base+tc.body)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want ValidationError, got %v", err)
			}
			if ve.Field != tc.field {
				t.Fatalf("field = %q (%s), want %q", ve.Field, ve.Reason, tc.field)
			}
		})
	}
}

func TestSyntheticBadGlobFieldStable(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
    rules:
      - match: { columns: ["a\\"], types: ["b\\"] }
        generator: redact
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
`
	// Repeat because a map-ordered walk passes a single run by chance.
	for i := 0; i < 50; i++ {
		_, err := decodeSyn(t, doc)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("want ValidationError, got %v", err)
		}
		if ve.Field != "spec.synthetic.rules[0].match.columns" {
			t.Fatalf("run %d: field = %q, want match.columns", i, ve.Field)
		}
	}
}

func TestSyntheticStrictUnknownField(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "    source: crm\n", "    source: crm\n    sorce: x\n", 1)
	if _, err := decodeSyn(t, doc); err == nil || !strings.Contains(err.Error(), "sorce") {
		t.Fatalf("want unknown field error, got %v", err)
	}
}

func TestSyntheticRelationErrors(t *testing.T) {
	t.Parallel()
	base := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
`
	cases := []struct {
		name, body, reason string
	}{
		{"rule", "        rules:\n          - match: { columns: [email] }\n            generator: template\n            args: { template: \"{a}\" }\n",
			"template refers to other columns, set it under tables[].columns"},
		// d reads the cycle but is not part of it.
		{"cycle", "        columns:\n" +
			"          d: { generator: template, args: { template: \"{a}\" } }\n" +
			"          a: { generator: template, args: { template: \"{b}\" } }\n" +
			"          b: { generator: template, args: { template: \"{c} {x}\" } }\n" +
			"          c: { generator: template, args: { template: \"{a}\" } }\n",
			"columns a, b, c depend on each other"},
		{"self", "        columns:\n          a: { generator: template, args: { template: \"{A}\" } }\n", "refers to the column itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Repeat because a map-ordered walk passes a single run by chance.
			for range 20 {
				_, err := decodeSyn(t, base+tc.body)
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("want ValidationError, got %v", err)
				}
				if ve.Reason != tc.reason {
					t.Fatalf("reason = %q, want %q", ve.Reason, tc.reason)
				}
			}
		})
	}
}

func TestSyntheticTemplateChain(t *testing.T) {
	t.Parallel()
	doc := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
        columns:
          a: { generator: template, args: { template: "{b}" } }
          b: { generator: template, args: { template: "{c}" } }
          c: { generator: fake, args: { kind: first_name } }
`
	if _, err := decodeSyn(t, doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestParseTemplate(t *testing.T) {
	t.Parallel()
	lits, refs, err := ParseTemplate("{{id}} {First}.{last}@x}}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lits, "|") != "{id} |.|@x}" || strings.Join(refs, "|") != "First|last" {
		t.Errorf("lits = %q, refs = %q", lits, refs)
	}
	for _, bad := range []string{"", "x", "{a", "{}", "{ }", "{a{b}}", "a}", "{{a}}"} {
		if _, _, err := ParseTemplate(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestSyntheticColumnRefs(t *testing.T) {
	t.Parallel()
	c := SyntheticColumn{Generator: GenTemplate, Args: map[string]any{"template": "{Zed}{a}-{A}{b}"}}
	if got := strings.Join(c.Refs(), ","); got != "a,b,zed" {
		t.Errorf("refs = %s", got)
	}
	if refs := (SyntheticColumn{Generator: GenFake, Args: map[string]any{"template": "{a}"}}).Refs(); refs != nil {
		t.Errorf("fake refs = %v", refs)
	}
}
