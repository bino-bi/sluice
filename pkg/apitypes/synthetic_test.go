// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// synDoc returns a synthetic DataSource whose first table has one column
// "c" with the given generator line and args block (YAML, indented for
// the column). A second table crm.orders has no columns entries.
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
      - name: crm.orders
        size: { ratio: 0.2 }
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
		"distribution":        `{ generator: distribution, args: { decimals: 2, integer: false, smooth: true, nullRate: source, unique: true } }`,
		"uniform":             `{ generator: uniform, args: { min: 1, max: 5.5 } }`,
		"normal":              `{ generator: normal, args: { truncate: false, decimals: 0, integer: true } }`,
		"lognormal":           `{ generator: lognormal, args: { truncate: true } }`,
		"histogram":           `{ generator: histogram, args: { bins: 20 } }`,
		"bucket edges":        `{ generator: bucket, args: { edges: [0, 10, 20.5] } }`,
		"bucket n":            `{ generator: bucket, args: { buckets: 5 } }`,
		"constant":            `{ generator: constant, args: { value: "x", nullRate: 0.1 } }`,
		"sequence":            `{ generator: sequence, args: { start: 100, step: -2 } }`,
		"range":               `{ generator: range, args: { min: 0, max: 10, decimals: 1 } }`,
		"categorical":         `{ generator: categorical, args: { otherLabel: other, maxValues: 10 } }`,
		"uniformCategory":     `{ generator: uniformCategory }`,
		"weighted":            `{ generator: weighted, args: { values: { a: 1, "Y": 2.5 } } }`,
		"boolean":             `{ generator: boolean, args: { trueRate: 0.3 } }`,
		"shuffleLabels":       `{ generator: shuffleLabels, args: { maxValues: 50 } }`,
		"randomString":        `{ generator: randomString, args: { length: { min: 3, max: 9 }, initial: "Q", charset: alnum, case: title } }`,
		"pattern":             `{ generator: pattern, args: { maxPatterns: 5 } }`,
		"regex":               `{ generator: regex, args: { pattern: "[A-Z]{2}-[0-9]{4}" } }`,
		"fake":                `{ generator: fake, args: { kind: city, locale: en, keepInitial: true } }`,
		"uuid":                `{ generator: uuid, args: { version: 7 } }`,
		"prefixKeep":          `{ generator: prefixKeep, args: { prefixLength: 3 } }`,
		"loremText":           `{ generator: loremText, args: { words: 12, vocabulary: de } }`,
		"randomText":          `{ generator: randomText, args: { length: source, vocabulary: lorem } }`,
		"redact":              `{ generator: redact, args: { value: "***" } }`,
		"dateDistribution":    `{ generator: dateDistribution, args: { granularity: month, keepWeekday: true, keepSeasonality: true } }`,
		"dateRange":           `{ generator: dateRange, args: { min: "2020-01-01", max: "2024-06-30T12:00:00Z", granularity: day } }`,
		"null":                `{ generator: "null" }`,
		"template":            `{ generator: template, args: { template: "{First_Name}.{last_name}@example.org {{x}}", lower: true, nullRate: 0.1 } }`,
		"dateOffset":          `{ generator: dateOffset, args: { from: Ordered_At } }`,
		"dateOffset-span":     `{ generator: dateOffset, args: { from: ordered_at, offset: { min: -36500, max: 0.5 }, nullRate: source, unique: true } }`,
		"dateOffset-src":      `{ generator: dateOffset, args: { from: ordered_at, offset: source } }`,
		"distribution-by":     `{ generator: distribution, args: { by: [status, Region, day] } }`,
		"categorical-by":      `{ generator: categorical, args: { by: [status] } }`,
		"dateDistribution-by": `{ generator: dateDistribution, args: { by: [status], keepWeekday: true } }`,
		"expression":          `{ generator: expression, args: { sql: "quantity * unit_price - 1", nullRate: 0.1 } }`,
		"foreignKey":          `{ generator: foreignKey, args: { table: CRM.Orders, column: id } }`,
		"foreignKey-args":     `{ generator: foreignKey, args: { table: crm.orders, column: Id, fanOut: { min: 0, max: 100000 }, orphanRate: 0.05, nullRate: source } }`,
		"foreignKey-source":   `{ generator: foreignKey, args: { table: crm.orders, column: id, fanOut: source, orphanRate: source } }`,
		"sourceVocabulary":    `{ generator: sourceVocabulary, args: { minWordCount: 2, words: { min: 3, max: 9 }, maxWords: 10000, nullRate: source } }`,
		"sourceVocabulary-0":  `{ generator: sourceVocabulary }`,
		"sentenceShape":       `{ generator: sentenceShape, args: { vocabulary: de } }`,
		"sentenceShape-src":   `{ generator: sentenceShape, args: { vocabulary: source, minWordCount: 1000000, maxWords: 1 } }`,
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
        privacy: { minGroupSize: 10, extremeJitter: { min: 0.01, max: 0.02 }, rejectExactMatches: [first_name, Last_Name] }
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
	if got := syn.Tables[0].Privacy.RejectExactMatches; len(got) != 2 || got[0] != "first_name" || got[1] != "Last_Name" {
		t.Errorf("rejectExactMatches = %v", got)
	}
	if r := syn.Tables[0].Size.Rows; r == nil || *r != 10000 {
		t.Errorf("rows = %v", r)
	}
	if got := syn.Tables[0].Columns["revenue"].Args["decimals"]; got != float64(2) {
		t.Errorf("decimals = %#v", got)
	}
}

func TestSyntheticNoiseValid(t *testing.T) {
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
    privacy: { noise: { epsilon: 0.5 } }
    tables:
      - name: crm.customers
        size: { ratio: 0.2 }
        columns:
          revenue: { generator: distribution, privacy: { bounds: { min: 0, max: 6000 } } }
          opened: { generator: dateDistribution, privacy: { bounds: { min: "2000-01-01", max: "2024-12-31T23:59:59" } } }
`
	ds, err := decodeSyn(t, doc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	syn := ds.Spec.Synthetic
	if n := syn.Privacy.Noise; n == nil || n.Epsilon != 0.5 {
		t.Errorf("noise = %+v", n)
	}
	if b := syn.Tables[0].Columns["revenue"].Privacy.Bounds; b == nil || b.Min != float64(0) || b.Max != float64(6000) {
		t.Errorf("revenue bounds = %+v", b)
	}
	if b := syn.Tables[0].Columns["opened"].Privacy.Bounds; b == nil || b.Min != "2000-01-01" || b.Max != "2024-12-31T23:59:59" {
		t.Errorf("opened bounds = %+v", b)
	}
	for _, eps := range []string{"0.001", "100"} {
		d := strings.Replace(doc, "epsilon: 0.5", "epsilon: "+eps, 1)
		if _, err := decodeSyn(t, d); err != nil {
			t.Errorf("epsilon %s: %v", eps, err)
		}
	}
}

// Bounds errors name the field, never the values.
func TestSyntheticBoundsReasonHidesValues(t *testing.T) {
	t.Parallel()
	doc := synDoc("crmsynth", `{ generator: distribution, privacy: { bounds: { min: 4711, max: "2024-03-17" } } }`)
	doc = strings.Replace(doc, "    tables:\n", "    privacy: { noise: { epsilon: 1 } }\n    tables:\n", 1)
	_, err := decodeSyn(t, doc)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if ve.Field != "spec.synthetic.tables[0].columns.c.privacy.bounds.max" {
		t.Fatalf("field = %q", ve.Field)
	}
	if strings.Contains(err.Error(), "4711") || strings.Contains(err.Error(), "2024") {
		t.Fatalf("error holds a value: %v", err)
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
		{"dateOffset no from", synDoc("crmsynth", `{ generator: dateOffset }`), "spec.synthetic.tables[0].columns.c.args.from"},
		{"dateOffset empty from", synDoc("crmsynth", `{ generator: dateOffset, args: { from: " " } }`), "spec.synthetic.tables[0].columns.c.args.from"},
		{"dateOffset from not a string", synDoc("crmsynth", `{ generator: dateOffset, args: { from: 3 } }`), "spec.synthetic.tables[0].columns.c.args.from"},
		{"dateOffset self ref", synDoc("crmsynth", `{ generator: dateOffset, args: { from: C } }`), "spec.synthetic.tables[0].columns.c.args.from"},
		{"dateOffset offset word", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: days } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset number", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: 3 } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset no max", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: { min: 1 } } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset extra key", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: { min: 1, max: 2, unit: 3 } } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset min above max", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: { min: 2, max: 1 } } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset too large", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: { min: 0, max: 36501 } } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset offset too small", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, offset: { min: -36501, max: 0 } } }`), "spec.synthetic.tables[0].columns.c.args.offset"},
		{"dateOffset unknown arg", synDoc("crmsynth", `{ generator: dateOffset, args: { from: a, days: 3 } }`), "spec.synthetic.tables[0].columns.c.args.days"},
		{"by on uniform", synDoc("crmsynth", `{ generator: uniform, args: { by: [a] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by empty", synDoc("crmsynth", `{ generator: distribution, args: { by: [] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by four", synDoc("crmsynth", `{ generator: distribution, args: { by: [a, b, d, e] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by twice", synDoc("crmsynth", `{ generator: categorical, args: { by: [a, A] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by not a list", synDoc("crmsynth", `{ generator: categorical, args: { by: a } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by empty name", synDoc("crmsynth", `{ generator: categorical, args: { by: [" "] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"by number", synDoc("crmsynth", `{ generator: categorical, args: { by: [3] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"expression no sql", synDoc("crmsynth", `{ generator: expression }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression empty sql", synDoc("crmsynth", `{ generator: expression, args: { sql: " " } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression sql number", synDoc("crmsynth", `{ generator: expression, args: { sql: 3 } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression semicolon", synDoc("crmsynth", `{ generator: expression, args: { sql: "a; DROP TABLE t" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression NUL", synDoc("crmsynth", `{ generator: expression, args: { sql: "a\0" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression line comment", synDoc("crmsynth", `{ generator: expression, args: { sql: "quantity -- x" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression block comment", synDoc("crmsynth", `{ generator: expression, args: { sql: "quantity /* x */" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression newline", synDoc("crmsynth", `{ generator: expression, args: { sql: "quantity\n+ 1" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression carriage return", synDoc("crmsynth", `{ generator: expression, args: { sql: "quantity\r+ 1" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression too long", synDoc("crmsynth", `{ generator: expression, args: { sql: "a`+strings.Repeat(" ", 1024)+`" } }`), "spec.synthetic.tables[0].columns.c.args.sql"},
		{"expression unique", synDoc("crmsynth", `{ generator: expression, args: { sql: "a", unique: true } }`), "spec.synthetic.tables[0].columns.c.args.unique"},
		{"expression unique false", synDoc("crmsynth", `{ generator: expression, args: { sql: "a", unique: false } }`), "spec.synthetic.tables[0].columns.c.args.unique"},
		{"by self ref", synDoc("crmsynth", `{ generator: dateDistribution, args: { by: [a, C] } }`), "spec.synthetic.tables[0].columns.c.args.by"},
		{"foreignKey no table", synDoc("crmsynth", `{ generator: foreignKey, args: { column: id } }`), "spec.synthetic.tables[0].columns.c.args.table"},
		{"foreignKey no column", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders } }`), "spec.synthetic.tables[0].columns.c.args.column"},
		{"foreignKey table shape", synDoc("crmsynth", `{ generator: foreignKey, args: { table: orders, column: id } }`), "spec.synthetic.tables[0].columns.c.args.table"},
		{"foreignKey table three parts", synDoc("crmsynth", `{ generator: foreignKey, args: { table: a.b.c, column: id } }`), "spec.synthetic.tables[0].columns.c.args.table"},
		{"foreignKey unknown table", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.nope, column: id } }`), "spec.synthetic.tables[0].columns.c.args.table"},
		{"foreignKey own table", synDoc("crmsynth", `{ generator: foreignKey, args: { table: CRM.customers, column: id } }`), "spec.synthetic.tables[0].columns.c.args.table"},
		{"foreignKey unique", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, unique: true } }`), "spec.synthetic.tables[0].columns.c.args.unique"},
		{"foreignKey fanOut number", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, fanOut: 3 } }`), "spec.synthetic.tables[0].columns.c.args.fanOut"},
		{"foreignKey fanOut too large", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, fanOut: { min: 0, max: 100001 } } }`), "spec.synthetic.tables[0].columns.c.args.fanOut"},
		{"foreignKey fanOut negative", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, fanOut: { min: -1, max: 2 } } }`), "spec.synthetic.tables[0].columns.c.args.fanOut"},
		{"foreignKey fanOut min above max", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, fanOut: { min: 3, max: 2 } } }`), "spec.synthetic.tables[0].columns.c.args.fanOut"},
		{"foreignKey orphanRate", synDoc("crmsynth", `{ generator: foreignKey, args: { table: crm.orders, column: id, orphanRate: 1.5 } }`), "spec.synthetic.tables[0].columns.c.args.orphanRate"},
		{"sourceVocabulary minWordCount 1", synDoc("crmsynth", `{ generator: sourceVocabulary, args: { minWordCount: 1 } }`), "spec.synthetic.tables[0].columns.c.args.minWordCount"},
		{"sourceVocabulary maxWords 0", synDoc("crmsynth", `{ generator: sourceVocabulary, args: { maxWords: 0 } }`), "spec.synthetic.tables[0].columns.c.args.maxWords"},
		{"sourceVocabulary words 0", synDoc("crmsynth", `{ generator: sourceVocabulary, args: { words: 0 } }`), "spec.synthetic.tables[0].columns.c.args.words"},
		{"sentenceShape vocabulary", synDoc("crmsynth", `{ generator: sentenceShape, args: { vocabulary: fr } }`), "spec.synthetic.tables[0].columns.c.args.vocabulary"},
		{"sentenceShape minWordCount lorem", synDoc("crmsynth", `{ generator: sentenceShape, args: { vocabulary: lorem, minWordCount: 5 } }`), "spec.synthetic.tables[0].columns.c.args.minWordCount"},
		{"sentenceShape maxWords default", synDoc("crmsynth", `{ generator: sentenceShape, args: { maxWords: 5 } }`), "spec.synthetic.tables[0].columns.c.args.maxWords"},
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
	noisy := "    privacy: { noise: { epsilon: 1 } }\n"
	bounded := func(b string) string {
		return "        columns:\n          a: { generator: distribution, privacy: { bounds: " + b + " } }\n"
	}
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
		{"dateOffset in source rule", "    rules:\n      - match: { types: [DATE] }\n        generator: dateOffset\n        args: { from: a }\n" + table, "spec.synthetic.rules[0].generator"},
		{"dateOffset in table rule", table + "        rules:\n          - match: { types: [DATE] }\n            generator: dateOffset\n            args: { from: a }\n", "spec.synthetic.tables[0].rules[0].generator"},
		{"dateOffset cycle", table + "        columns:\n          a: { generator: dateOffset, args: { from: b } }\n          b: { generator: template, args: { template: \"{A}\" } }\n", "spec.synthetic.tables[0].columns"},
		{"template cycle", table + "        columns:\n          a: { generator: template, args: { template: \"{b}\" } }\n          b: { generator: template, args: { template: \"{A}\" } }\n", "spec.synthetic.tables[0].columns"},
		{"expression in source rule", "    rules:\n      - match: { columns: [total] }\n        generator: expression\n        args: { sql: \"a\" }\n" + table, "spec.synthetic.rules[0].generator"},
		{"expression in table rule", table + "        rules:\n          - match: { types: [DOUBLE] }\n            generator: expression\n            args: { sql: \"a\" }\n", "spec.synthetic.tables[0].rules[0].generator"},
		{"template reads expression", table + "        columns:\n          a: { generator: template, args: { template: \"{B}\" } }\n          b: { generator: expression, args: { sql: \"a\" } }\n", "spec.synthetic.tables[0].columns.a.args.template"},
		{"dateOffset reads expression", table + "        columns:\n          a: { generator: dateOffset, args: { from: b } }\n          b: { generator: expression, args: { sql: \"c\" } }\n", "spec.synthetic.tables[0].columns.a.args.from"},
		{"by reads expression", table + "        columns:\n          a: { generator: categorical, args: { by: [c, b] } }\n          b: { generator: expression, args: { sql: \"c\" } }\n", "spec.synthetic.tables[0].columns.a.args.by"},
		{"by in source rule", "    rules:\n      - match: { columns: [amount] }\n        generator: distribution\n        args: { by: [status] }\n" + table, "spec.synthetic.rules[0].args.by"},
		{"by in table rule", table + "        rules:\n          - match: { types: [VARCHAR] }\n            generator: categorical\n            args: { by: [status] }\n", "spec.synthetic.tables[0].rules[0].args.by"},
		{"by cycle", table + "        columns:\n          a: { generator: categorical, args: { by: [b] } }\n          b: { generator: template, args: { template: \"{A}\" } }\n", "spec.synthetic.tables[0].columns"},
		{"foreignKey in source rule", "    rules:\n      - match: { columns: [\"*_id\"] }\n        generator: foreignKey\n        args: { table: crm.customers, column: id }\n" + table, "spec.synthetic.rules[0].generator"},
		{"foreignKey in table rule", table + "        rules:\n          - match: { columns: [\"*_id\"] }\n            generator: foreignKey\n            args: { table: crm.customers, column: id }\n", "spec.synthetic.tables[0].rules[0].generator"},
		{"source rejectExactMatches", "    privacy: { rejectExactMatches: [a] }\n" + table, "spec.synthetic.privacy.rejectExactMatches"},
		{"column rejectExactMatches", table + "        columns:\n          a: { generator: \"null\", privacy: { rejectExactMatches: [a] } }\n", "spec.synthetic.tables[0].columns.a.privacy.rejectExactMatches"},
		{"empty rejectExactMatches", "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { rejectExactMatches: [] }\n", "spec.synthetic.tables[0].privacy.rejectExactMatches"},
		{"duplicate rejectExactMatches", "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { rejectExactMatches: [a, A] }\n", "spec.synthetic.tables[0].privacy.rejectExactMatches"},
		{"rejectExactMatches names expression", "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { rejectExactMatches: [a, Total] }\n" +
			"        columns:\n          total: { generator: expression, args: { sql: \"a\" } }\n", "spec.synthetic.tables[0].privacy.rejectExactMatches"},
		{"table noise", "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { noise: { epsilon: 1 } }\n", "spec.synthetic.tables[0].privacy.noise"},
		{"column noise", table + "        columns:\n          a: { generator: distribution, privacy: { noise: { epsilon: 1 } } }\n", "spec.synthetic.tables[0].columns.a.privacy.noise"},
		{"epsilon 0", "    privacy: { noise: { epsilon: 0 } }\n" + table, "spec.synthetic.privacy.noise.epsilon"},
		{"epsilon missing", "    privacy: { noise: {} }\n" + table, "spec.synthetic.privacy.noise.epsilon"},
		{"epsilon 101", "    privacy: { noise: { epsilon: 101 } }\n" + table, "spec.synthetic.privacy.noise.epsilon"},
		{"epsilon below 0.001", "    privacy: { noise: { epsilon: 0.0009 } }\n" + table, "spec.synthetic.privacy.noise.epsilon"},
		{"epsilon above 100", "    privacy: { noise: { epsilon: 100.0001 } }\n" + table, "spec.synthetic.privacy.noise.epsilon"},
		{"source bounds", "    privacy: { noise: { epsilon: 1 }, bounds: { min: 0, max: 1 } }\n" + table, "spec.synthetic.privacy.bounds"},
		{"table bounds", noisy + "    tables:\n      - name: crm.customers\n        size: { ratio: 0.2 }\n        privacy: { bounds: { min: 0, max: 1 } }\n", "spec.synthetic.tables[0].privacy.bounds"},
		{"bounds without noise", table + bounded("{ min: 0, max: 1 }"), "spec.synthetic.tables[0].columns.a.privacy.bounds"},
		{"bounds mixed", noisy + table + bounded("{ min: 0, max: \"2024-01-01\" }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.max"},
		{"bounds date and number", noisy + table + bounded("{ min: \"2024-01-01\", max: 5 }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.max"},
		{"bounds equal", noisy + table + bounded("{ min: 5, max: 5 }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.max"},
		{"bounds dates reversed", noisy + table + bounded("{ min: \"2024-02-01\", max: \"2024-01-01\" }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.max"},
		{"bounds no max", noisy + table + bounded("{ min: 0 }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.max"},
		{"bounds no min", noisy + table + bounded("{ max: 1 }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.min"},
		{"bounds bad text", noisy + table + bounded("{ min: soon, max: later }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.min"},
		{"bounds bool", noisy + table + bounded("{ min: true, max: false }"), "spec.synthetic.tables[0].columns.a.privacy.bounds.min"},
		{"foreignKey cycle", "    tables:\n" +
			"      - name: crm.a\n        size: { ratio: 0.2 }\n        columns:\n          b_id: { generator: foreignKey, args: { table: crm.b, column: id } }\n" +
			"      - name: crm.b\n        size: { ratio: 0.2 }\n        columns:\n          a_id: { generator: foreignKey, args: { table: CRM.A, column: id } }\n",
			"spec.synthetic.tables"},
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
		{"dateOffset rule", "        rules:\n          - match: { types: [DATE] }\n            generator: dateOffset\n            args: { from: a }\n",
			"dateOffset refers to other columns, set it under tables[].columns"},
		{"dateOffset cycle", "        columns:\n" +
			"          shipped: { generator: dateOffset, args: { from: Ordered } }\n" +
			"          ordered: { generator: dateOffset, args: { from: shipped } }\n",
			"columns ordered, shipped depend on each other"},
		{"by rule", "        rules:\n          - match: { columns: [amount] }\n            generator: distribution\n            args: { by: [status] }\n",
			"by refers to other columns, set it under tables[].columns"},
		{"by cycle", "        columns:\n" +
			"          status: { generator: categorical, args: { by: [Dept] } }\n" +
			"          dept: { generator: categorical, args: { by: [region, status] } }\n",
			"columns dept, status depend on each other"},
		{"expression rule", "        rules:\n          - match: { columns: [total] }\n            generator: expression\n            args: { sql: \"a\" }\n",
			"expression refers to other columns, set it under tables[].columns"},
		{"template reads expression", "        columns:\n" +
			"          email: { generator: template, args: { template: \"{Total}\" } }\n" +
			"          total: { generator: expression, args: { sql: \"email\" } }\n",
			"names expression column total, which is computed after the other columns"},
		{"by self", "        columns:\n          a: { generator: distribution, args: { by: [b, A] } }\n", "refers to the column itself"},
		{"by twice", "        columns:\n          a: { generator: distribution, args: { by: [b, B] } }\n", "names column B twice"},
		{"rejectExactMatches expression", "        privacy: { rejectExactMatches: [a, Total] }\n        columns:\n          total: { generator: expression, args: { sql: \"a\" } }\n",
			"names expression column total, which is computed after the check"},
		{"rejectExactMatches twice", "        privacy: { rejectExactMatches: [a, A] }\n", "names column A twice"},
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
	c = SyntheticColumn{Generator: GenDateOffset, Args: map[string]any{"from": "Ordered_At"}}
	if got := strings.Join(c.Refs(), ","); got != "ordered_at" {
		t.Errorf("dateOffset refs = %s", got)
	}
	c = SyntheticColumn{Generator: GenCategorical, Args: map[string]any{"by": []any{"Status", "dept"}}}
	if got := strings.Join(c.Refs(), ","); got != "dept,status" {
		t.Errorf("by refs = %s", got)
	}
	if refs := (SyntheticColumn{Generator: GenFake, Args: map[string]any{"template": "{a}", "from": "b", "by": []any{"c"}}}).Refs(); refs != nil {
		t.Errorf("fake refs = %v", refs)
	}
}

func TestSyntheticTableOrder(t *testing.T) {
	t.Parallel()
	fk := func(table string) map[string]SyntheticColumn {
		return map[string]SyntheticColumn{"p_id": {Generator: GenForeignKey, Args: map[string]any{"table": table, "column": "id"}}}
	}
	// orders and items come before their parents in the list.
	spec := &SyntheticSpec{Tables: []SyntheticTable{
		{Name: "crm.items", Columns: fk("crm.orders")},
		{Name: "crm.orders", Columns: fk("CRM.Customers")},
		{Name: "crm.other"},
		{Name: "crm.customers"},
	}}
	order, err := SyntheticTableOrder(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[2 3 1 0]" {
		t.Errorf("order = %s, want [2 3 1 0]", got)
	}
	// No foreignKey: list order.
	plain := &SyntheticSpec{Tables: []SyntheticTable{{Name: "s.b"}, {Name: "s.a"}}}
	if order, err := SyntheticTableOrder(plain); err != nil || fmt.Sprint(order) != "[0 1]" {
		t.Errorf("order = %v, %v", order, err)
	}

	for _, tc := range []struct {
		name   string
		tables []SyntheticTable
		want   string
	}{
		{"cycle", []SyntheticTable{
			{Name: "crm.x", Columns: fk("crm.c")},
			{Name: "crm.c", Columns: fk("crm.b")},
			{Name: "crm.b", Columns: fk("crm.a")},
			{Name: "crm.a", Columns: fk("crm.c")},
		}, "foreignKey cycle between tables crm.a, crm.b, crm.c"},
		{"self", []SyntheticTable{{Name: "crm.a", Columns: fk("crm.A")}}, "table crm.a: column p_id names its own table"},
		{"unknown", []SyntheticTable{{Name: "crm.a", Columns: fk("crm.z")}}, "table crm.a: column p_id names table crm.z, which is not in spec.synthetic.tables"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for range 20 {
				_, err := SyntheticTableOrder(&SyntheticSpec{Tables: tc.tables})
				if err == nil || err.Error() != tc.want {
					t.Fatalf("err = %v, want %q", err, tc.want)
				}
			}
		})
	}
}

func TestSyntheticForeignKeyReasons(t *testing.T) {
	t.Parallel()
	doc := func(cols string) string {
		return strings.Replace(synDoc("crmsynth", `{ generator: "null" }`), "          c: { generator: \"null\" }\n", cols, 1)
	}
	for _, tc := range []struct{ name, doc, reason string }{
		{"own table", doc("          c: { generator: foreignKey, args: { table: crm.customers, column: id } }\n"),
			"names its own table, a foreignKey must point to another table"},
		{"unknown table", doc("          c: { generator: foreignKey, args: { table: crm.nope, column: id } }\n"),
			"names table crm.nope, which is not in spec.synthetic.tables"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeSyn(t, tc.doc)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Reason != tc.reason {
				t.Fatalf("err = %v, want reason %q", err, tc.reason)
			}
		})
	}
	cycle := `apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: crmsynth
spec:
  type: synthetic
  synthetic:
    source: crm
    seed: secret://env/SEED
    tables:
      - name: public.b
        size: { ratio: 0.2 }
        columns:
          a_id: { generator: foreignKey, args: { table: public.a, column: id } }
      - name: public.a
        size: { ratio: 0.2 }
        columns:
          b_id: { generator: foreignKey, args: { table: public.b, column: id } }
`
	_, err := decodeSyn(t, cycle)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "spec.synthetic.tables" || ve.Reason != "foreignKey cycle between tables public.a, public.b" {
		t.Fatalf("err = %v", err)
	}
}
