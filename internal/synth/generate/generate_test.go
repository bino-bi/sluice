// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

var updateGolden = flag.Bool("update", false, "overwrite golden files with observed output")

var testSeed = []byte("generate-test-seed-0123456789")

func streams(col string) Streams {
	f := func(p string) *rng.Stream { return rng.For(testSeed, "syn", "crm.t", col, p) }
	return Streams{Values: f(rng.PurposeValues), Nulls: f(rng.PurposeNulls), Shuffle: f(rng.PurposeShuffle)}
}

// goCast stands in for the builder's DuckDB cast.
func goCast(typ string) func(string) (any, error) {
	return func(s string) (any, error) {
		switch profile.KindOf(typ) {
		case profile.KindInteger:
			return strconv.ParseInt(s, 10, 64)
		case profile.KindFloat:
			return strconv.ParseFloat(s, 64)
		case profile.KindBool:
			return strconv.ParseBool(s)
		case profile.KindDate:
			return time.Parse("2006-01-02", s)
		}
		return s, nil
	}
}

func col(name, typ string) Column {
	return Column{Name: name, Type: typ, Rows: 1000, Cast: goCast(typ)}
}

// numberProfile is a filtered profile on the number axis [10, 110].
func numberProfile() *profile.Column {
	return &profile.Column{
		Counts:    profile.Counts{Rows: 1000, NonNull: 950, Distinct: 300},
		NullRate:  0.05,
		Lo:        10,
		Hi:        110,
		Mean:      50,
		StdDev:    15,
		Quantiles: []float64{10, 20, 35, 50, 70, 110},
		LogMean:   3.8,
		LogStdDev: 0.4,
		Bins:      []profile.Bin{{Lo: 10, Hi: 40, Count: 300}, {Lo: 40, Hi: 80, Count: 500}, {Lo: 80, Hi: 110, Count: 150}},
	}
}

func textProfile() *profile.Column {
	nom := func(vs ...any) profile.Nominal {
		var out profile.Nominal
		for i := 0; i < len(vs); i += 2 {
			out.Values = append(out.Values, profile.Value{V: vs[i], Count: int64(vs[i+1].(int))})
		}
		return out
	}
	p := &profile.Column{
		Counts:      profile.Counts{Rows: 1000, NonNull: 1000, Distinct: 400},
		TopValues:   nom("M", 500, "W", 400, "D", 97),
		Initials:    nom("S", 300, "M", 200, "K", 100),
		Patterns:    nom("AA-9999", 400, "Aa-99a", 100),
		Prefixes:    nom("DE", 300, "AT", 100),
		Lengths:     []profile.Bin{{Lo: 6, Hi: 6, Count: 200}, {Lo: 8, Hi: 8, Count: 300}, {Lo: 12, Hi: 12, Count: 100}},
		WordCounts:  []profile.Bin{{Lo: 1, Hi: 1, Count: 100}, {Lo: 3, Hi: 3, Count: 200}},
		CharClasses: profile.CharClasses{Upper: true, Lower: true, Digit: true},
		CaseStyles:  [4]int64{100, 200, 300, 0},
	}
	p.TopValues.Rest = 3
	return p
}

// dateProfile is on the DATE axis (days): 2020-01-01 to 2021-01-01.
func dateProfile() *profile.Column {
	return &profile.Column{
		Counts:    profile.Counts{Rows: 1000, NonNull: 1000, Distinct: 366},
		Lo:        18262,
		Hi:        18628,
		Quantiles: []float64{18262, 18350, 18450, 18628},
		Weekdays:  profile.Nominal{Values: []profile.Value{{V: int64(1), Count: 300}, {V: int64(5), Count: 200}}},
		Months:    profile.Nominal{Values: []profile.Value{{V: int64(3), Count: 300}, {V: int64(12), Count: 100}}},
	}
}

// tsProfile is on the TIMESTAMP axis (seconds) over 2020.
func tsProfile() *profile.Column {
	p := dateProfile()
	p.Lo, p.Hi = 1577836800, 1609459200
	p.Quantiles = []float64{1577836800, 1590000000, 1600000000, 1609459200}
	return p
}

func boolProfile() *profile.Column {
	return &profile.Column{
		Counts:    profile.Counts{Rows: 100, NonNull: 100, Distinct: 2},
		TopValues: profile.Nominal{Values: []profile.Value{{V: true, Count: 60}, {V: false, Count: 40}}},
	}
}

type genCase struct {
	name string
	gen  apitypes.SyntheticGenerator
	col  Column
	args Args
	p    *profile.Column
}

func cases() []genCase {
	return []genCase{
		{"distribution", apitypes.GenDistribution, col("revenue", "DECIMAL(12,2)"), Args{"decimals": 2.0}, numberProfile()},
		{"distribution-int", apitypes.GenDistribution, col("age", "INTEGER"), nil, numberProfile()},
		{"distribution-steps", apitypes.GenDistribution, col("score", "DOUBLE"), Args{"smooth": false}, numberProfile()},
		{"distribution-time", apitypes.GenDistribution, col("opened", "TIME"), nil, numberProfile()},
		{"uniform", apitypes.GenUniform, col("u", "DOUBLE"), Args{"min": 1.0, "max": 2.0}, nil},
		{"normal", apitypes.GenNormal, col("n", "DOUBLE"), Args{"decimals": 1.0}, numberProfile()},
		{"lognormal", apitypes.GenLognormal, col("l", "DOUBLE"), Args{"decimals": 3.0}, numberProfile()},
		{"histogram", apitypes.GenHistogram, col("h", "BIGINT"), nil, numberProfile()},
		{"bucket-edges", apitypes.GenBucket, col("b", "INTEGER"), Args{"edges": []any{20.0, 40.0, 60.0}}, numberProfile()},
		{"bucket-n", apitypes.GenBucket, col("b2", "DOUBLE"), Args{"buckets": 4.0}, numberProfile()},
		{"constant", apitypes.GenConstant, col("c", "INTEGER"), Args{"value": 7.0}, nil},
		{"sequence", apitypes.GenSequence, col("id", "BIGINT"), Args{"start": 100.0, "step": 5.0}, nil},
		{"sequence-text", apitypes.GenSequence, col("code", "VARCHAR"), nil, nil},
		{"range", apitypes.GenRange, col("r", "DOUBLE"), Args{"min": 0.0, "max": 1.0, "decimals": 2.0}, nil},
		{"categorical", apitypes.GenCategorical, col("gender", "VARCHAR"), Args{"otherLabel": "other"}, textProfile()},
		{"uniformCategory", apitypes.GenUniformCategory, col("gender2", "VARCHAR"), nil, textProfile()},
		{"weighted", apitypes.GenWeighted, col("segment", "VARCHAR"), Args{"values": map[string]any{"a": 1.0, "b": 2.0, "c": 3.0}}, nil},
		{"boolean", apitypes.GenBoolean, col("active", "BOOLEAN"), nil, boolProfile()},
		{"shuffleLabels", apitypes.GenShuffleLabels, col("gender3", "VARCHAR"), nil, textProfile()},
		{"randomString", apitypes.GenRandomString, col("last_name", "VARCHAR"), Args{"initial": "source", "charset": "source"}, textProfile()},
		{"pattern", apitypes.GenPattern, col("code2", "VARCHAR"), nil, textProfile()},
		{"regex", apitypes.GenRegex, col("re", "VARCHAR"), Args{"pattern": `[A-Z]{2}-\d{3}(x|y)?`}, nil},
		{"fake-name", apitypes.GenFake, col("name", "VARCHAR"), Args{"kind": "name", "keepInitial": true}, textProfile()},
		{"fake-email", apitypes.GenFake, col("email", "VARCHAR"), Args{"kind": "email"}, nil},
		{"fake-phone", apitypes.GenFake, col("phone", "VARCHAR"), Args{"kind": "phone"}, nil},
		{"fake-street", apitypes.GenFake, col("street", "VARCHAR"), Args{"kind": "street"}, nil},
		{"fake-iban", apitypes.GenFake, col("iban", "VARCHAR"), Args{"kind": "iban"}, nil},
		{"uuid", apitypes.GenUUID, col("uid", "UUID"), nil, nil},
		{"uuid7", apitypes.GenUUID, col("uid7", "VARCHAR"), Args{"version": 7.0}, nil},
		{"prefixKeep", apitypes.GenPrefixKeep, col("iban2", "VARCHAR"), Args{"prefixLength": 2.0}, textProfile()},
		{"loremText", apitypes.GenLoremText, col("notes", "VARCHAR"), nil, textProfile()},
		{"randomText", apitypes.GenRandomText, col("notes2", "VARCHAR"), Args{"vocabulary": "lorem"}, textProfile()},
		{"redact", apitypes.GenRedact, col("x", "VARCHAR"), nil, nil},
		{"dateDistribution", apitypes.GenDateDistribution, col("birth", "DATE"), Args{"keepWeekday": true, "keepSeasonality": true}, dateProfile()},
		{"dateDistribution-ts", apitypes.GenDateDistribution, col("seen", "TIMESTAMP WITH TIME ZONE"), Args{"granularity": "hour"}, tsProfile()},
		{"dateRange", apitypes.GenDateRange, col("d", "DATE"), Args{"min": "2024-01-01", "max": "2024-12-31"}, nil},
		{"null", apitypes.GenNull, col("blob", "BLOB"), nil, nil},
		{"nullRate", apitypes.GenSequence, col("id2", "BIGINT"), Args{"nullRate": 0.5}, nil},
	}
}

func draw(t *testing.T, c genCase, n int) []any {
	t.Helper()
	g, ok := Lookup(c.gen)
	if !ok {
		t.Fatalf("%s: unknown generator", c.name)
	}
	if err := g.Check(c.col, c.args); err != nil {
		t.Fatalf("%s: Check: %v", c.name, err)
	}
	cg, err := New(c.gen, c.col, c.p, c.args, streams(c.col.Name))
	if err != nil {
		t.Fatalf("%s: New: %v", c.name, err)
	}
	out := make([]any, n)
	for i := range out {
		if out[i], err = cg.Next(RowCtx{Index: int64(i)}); err != nil {
			t.Fatalf("%s: Next: %v", c.name, err)
		}
	}
	return out
}

// TestGolden pins the first values of every generator for a fixed
// profile and seed (plan test 15). Run with -update to rewrite.
func TestGolden(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("golden values are pinned on amd64; math.Exp and math.Log may differ in the last bit elsewhere")
	}
	var b strings.Builder
	for _, c := range cases() {
		vals := draw(t, c, 6)
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = fmt.Sprintf("%v", v)
			if tm, ok := v.(time.Time); ok {
				parts[i] = tm.Format(time.RFC3339)
			}
		}
		fmt.Fprintf(&b, "%s: %s\n", c.name, strings.Join(parts, " | "))
	}
	path := filepath.Join("testdata", "golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("golden values differ (run with -update after checking):\n got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestStageTypes(t *testing.T) {
	want := map[string]string{
		"distribution": "float64", "distribution-int": "int64", "distribution-time": "float64",
		"histogram": "int64", "bucket-edges": "string", "constant": "int64", "sequence-text": "string",
		"boolean": "bool", "uuid": "string", "dateDistribution": "time.Time", "dateDistribution-ts": "time.Time",
	}
	for _, c := range cases() {
		w, ok := want[c.name]
		if !ok {
			continue
		}
		for _, v := range draw(t, c, 50) {
			if v != nil && fmt.Sprintf("%T", v) != w {
				t.Errorf("%s: value type %T, want %s", c.name, v, w)
			}
		}
	}
}

func TestNumbersStayInRange(t *testing.T) {
	for _, c := range cases()[:10] {
		p := c.p
		for _, v := range draw(t, c, 2000) {
			var f float64
			switch x := v.(type) {
			case nil, string:
				continue
			case int64:
				f = float64(x)
			case float64:
				f = x
			}
			lo, hi := 10.0, 110.0
			if p == nil {
				lo, hi = 1, 2
			}
			if f < lo || f > hi {
				t.Fatalf("%s: %v outside [%v, %v]", c.name, f, lo, hi)
			}
		}
	}
}

// The jitter can move the bounds past a type limit; the draws must not
// follow, or the builder's CAST fails (UINTEGER, DECIMAL) or wraps (TIME).
func TestNumbersStayInTypeRange(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		lo, hi float64
		wantLo float64
		wantHi float64
	}{
		{"UINTEGER", -3, 50, 0, 50},
		{"UTINYINT", -2, 260, 0, 255},
		{"TINYINT", -130, 129, -128, 127},
		{"DECIMAL(4,2)", -101, 101, -99.99, 99.99},
		{"TIME", -500, 87000, 0, secondsPerDay - 1e-6},
	} {
		p := &profile.Column{
			Lo: tc.lo, Hi: tc.hi, Mean: 0, StdDev: 1,
			Quantiles: []float64{tc.lo, tc.lo, tc.hi, tc.hi},
			Bins:      []profile.Bin{{Lo: tc.lo, Hi: tc.lo, Count: 10}, {Lo: tc.hi, Hi: tc.hi, Count: 10}},
		}
		c := col("n", tc.typ)
		for _, g := range []apitypes.SyntheticGenerator{apitypes.GenDistribution, apitypes.GenUniform, apitypes.GenHistogram} {
			if g == apitypes.GenHistogram && profile.KindOf(tc.typ) == profile.KindTime {
				continue
			}
			gen, err := New(g, c, p, nil, streams("n"))
			if err != nil {
				t.Fatalf("%s %s: %v", tc.typ, g, err)
			}
			for range 500 {
				v, _ := gen.Next(RowCtx{})
				f, _, _, _ := number(v)
				if f < tc.wantLo || f > tc.wantHi {
					t.Fatalf("%s %s: %v outside [%v, %v]", tc.typ, g, f, tc.wantLo, tc.wantHi)
				}
			}
		}
	}
	if lo, hi := typeRange(col("n", "DOUBLE")); !math.IsInf(lo, -1) || !math.IsInf(hi, 1) {
		t.Errorf("DOUBLE range = [%v, %v]", lo, hi)
	}
}

func TestRegexOutputMatches(t *testing.T) {
	for _, pat := range []string{
		`[A-Z]{2}-\d{3}(x|y)?`,
		`^[a-f0-9]{8}$`,
		`(foo|bar)+\.baz*`,
		`[^a-y]{3}`,
		`\w+@\w+\.(com|org)`,
		`.{5}`,
		`[α-ω]{2,4}`,
	} {
		c := genCase{gen: apitypes.GenRegex, col: col("re", "VARCHAR"), args: Args{"pattern": pat}}
		re := regexp.MustCompile(`^(?:` + pat + `)$`)
		for _, v := range draw(t, c, 1000) {
			if !re.MatchString(v.(string)) {
				t.Fatalf("pattern %s: %q does not match", pat, v)
			}
		}
	}
}

func TestIBANCheckDigits(t *testing.T) {
	c := genCase{gen: apitypes.GenFake, col: col("iban", "VARCHAR"), args: Args{"kind": "iban"}}
	for _, v := range draw(t, c, 500) {
		s := v.(string)
		if len(s) != 22 || !strings.HasPrefix(s, "DE") {
			t.Fatalf("bad IBAN shape %q", s)
		}
		// ISO 13616: move the first four chars to the end, letters to
		// numbers (A=10), the result mod 97 is 1.
		r := s[4:] + s[:4]
		var digits strings.Builder
		for _, ch := range r {
			if ch >= 'A' && ch <= 'Z' {
				digits.WriteString(strconv.Itoa(int(ch-'A') + 10))
			} else {
				digits.WriteRune(ch)
			}
		}
		n, _ := new(big.Int).SetString(digits.String(), 10)
		if new(big.Int).Mod(n, big.NewInt(97)).Int64() != 1 {
			t.Fatalf("IBAN %q fails the check", s)
		}
	}
}

func TestUUIDBits(t *testing.T) {
	v4 := draw(t, genCase{gen: apitypes.GenUUID, col: col("u", "UUID")}, 200)
	for _, v := range v4 {
		s := v.(string)
		if len(s) != 36 || s[14] != '4' || !strings.ContainsRune("89ab", rune(s[19])) {
			t.Fatalf("not a v4 UUID: %s", s)
		}
	}
	v7 := draw(t, genCase{gen: apitypes.GenUUID, col: col("u", "VARCHAR"), args: Args{"version": 7.0}}, 200)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	prev := ""
	for i, v := range v7 {
		s := v.(string)
		if s[14] != '7' || !strings.ContainsRune("89ab", rune(s[19])) {
			t.Fatalf("not a v7 UUID: %s", s)
		}
		if s <= prev {
			t.Fatalf("v7 values not sorted at row %d", i)
		}
		prev = s
		ms, err := strconv.ParseInt(strings.ReplaceAll(s[:13], "-", ""), 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if ms < start || ms > start+msPerYear+int64(i) {
			t.Fatalf("v7 time field %d out of range", ms)
		}
	}
}

func TestUnique(t *testing.T) {
	// Known small pool: fails before generation.
	for _, c := range []genCase{
		{gen: apitypes.GenConstant, col: col("c", "INTEGER"), args: Args{"value": 1.0, "unique": true}},
		{gen: apitypes.GenCategorical, col: col("g", "VARCHAR"), args: Args{"unique": true}, p: textProfile()},
	} {
		_, err := New(c.gen, c.col, c.p, c.args, streams("c"))
		if err == nil || !strings.Contains(err.Error(), "unique needs 1000 values") {
			t.Errorf("%s: want a pool error, got %v", c.gen, err)
		}
	}
	// With nulls the pool check is skipped; the draw fails at run time.
	c := genCase{gen: apitypes.GenWeighted, col: col("w", "VARCHAR"), args: Args{"values": map[string]any{"a": 1.0, "b": 1.0}, "unique": true, "nullRate": 0.1}}
	cg, err := New(c.gen, c.col, nil, c.args, streams("w"))
	if err != nil {
		t.Fatal(err)
	}
	var runErr error
	seen := map[any]bool{}
	for i := range 100 {
		v, err := cg.Next(RowCtx{Index: int64(i)})
		if err != nil {
			runErr = err
			break
		}
		if v != nil && seen[v] {
			t.Fatalf("duplicate %v", v)
		}
		seen[v] = true
	}
	if runErr == nil || runErr.Error() != "column w: cannot draw unique values, 2 distinct values reached" {
		t.Errorf("run error = %v", runErr)
	}
	// A large pool stays unique.
	c = genCase{gen: apitypes.GenRandomString, col: col("s", "VARCHAR"), args: Args{"length": 12.0, "case": "lower", "unique": true}}
	seen = map[any]bool{}
	for _, v := range draw(t, c, 1000) {
		if seen[v] {
			t.Fatalf("duplicate %v", v)
		}
		seen[v] = true
	}
}

func TestPatternKeepsSeparatorsAndCase(t *testing.T) {
	p := textProfile()
	p.Patterns = profile.Nominal{Values: []profile.Value{{V: "Aa-99a", Count: 10}}}
	re := regexp.MustCompile(`^[A-Z][a-z]-[0-9]{2}[a-z]$`)
	for _, v := range draw(t, genCase{gen: apitypes.GenPattern, col: col("code", "VARCHAR"), p: p}, 500) {
		if !re.MatchString(v.(string)) {
			t.Fatalf("%q does not keep the pattern Aa-99a", v)
		}
	}
}

func TestWeightedOrderIndependent(t *testing.T) {
	keys := []string{"zeta", "alpha", "mid", "beta", "omega", "x"}
	build := func(order []int) map[string]any {
		m := map[string]any{}
		for _, i := range order {
			m[keys[i]] = float64(i + 1)
		}
		return m
	}
	a := genCase{gen: apitypes.GenWeighted, col: col("w", "VARCHAR"), args: Args{"values": build([]int{0, 1, 2, 3, 4, 5})}}
	b := genCase{gen: apitypes.GenWeighted, col: col("w", "VARCHAR"), args: Args{"values": build([]int{5, 3, 1, 4, 2, 0})}}
	for range 5 {
		if fmt.Sprint(draw(t, a, 300)) != fmt.Sprint(draw(t, b, 300)) {
			t.Fatal("weighted values depend on the map order")
		}
	}
	// Integer keys sort by value, not by text.
	ints := genCase{gen: apitypes.GenWeighted, col: col("n", "INTEGER"), args: Args{"values": map[string]any{"10": 1.0, "9": 1.0}}}
	for _, v := range draw(t, ints, 20) {
		if _, ok := v.(int64); !ok {
			t.Fatalf("integer key staged as %T", v)
		}
	}
	bad := genCase{gen: apitypes.GenWeighted, col: col("n", "INTEGER"), args: Args{"values": map[string]any{"secret-value": 1.0}}}
	_, err := New(bad.gen, bad.col, nil, bad.args, streams("n"))
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Errorf("bad key: want an error without the key, got %v", err)
	}
}

func TestBucketLabels(t *testing.T) {
	got := bucketLabels([]float64{30, 40, 50}, true)
	if strings.Join(got, ",") != "<30,30-39,40-49,>=50" {
		t.Errorf("integer labels: %v", got)
	}
	got = bucketLabels([]float64{0.5, 1.5}, false)
	if strings.Join(got, ",") != "<0.5,0.5-1.5,>=1.5" {
		t.Errorf("float labels: %v", got)
	}
	// buckets: n uses the inner quantiles at j/n, rounded to 2 digits:
	// 23.75, 42.5 and 65 on the grid of numberProfile.
	c := genCase{gen: apitypes.GenBucket, col: col("b", "DOUBLE"), args: Args{"buckets": 4.0}, p: numberProfile()}
	labels := map[any]bool{}
	for _, v := range draw(t, c, 2000) {
		if v != nil {
			labels[v] = true
		}
	}
	if len(labels) != 4 {
		t.Errorf("labels: %v", labels)
	}
	for _, want := range []string{"<24", "24-43", "43-65", ">=65"} {
		if !labels[want] {
			t.Errorf("label %q not drawn: %v", want, labels)
		}
	}
}

func TestDateKeep(t *testing.T) {
	c := genCase{gen: apitypes.GenDateDistribution, col: col("d", "DATE"), args: Args{"keepWeekday": true, "keepSeasonality": true}, p: dateProfile()}
	for _, v := range draw(t, c, 1000) {
		d := v.(time.Time)
		if wd := d.Weekday(); wd != time.Monday && wd != time.Friday {
			t.Fatalf("%s: weekday %s not in the source histogram", d.Format(time.DateOnly), wd)
		}
		if d.Hour() != 0 || d.Location() != time.UTC {
			t.Fatalf("%v: DATE value is not midnight UTC", d)
		}
	}
	ts := genCase{gen: apitypes.GenDateDistribution, col: col("t", "TIMESTAMP"), args: Args{"granularity": "month"}, p: tsProfile()}
	for _, v := range draw(t, ts, 200) {
		d := v.(time.Time)
		if d.Day() != 1 || d.Hour() != 0 {
			t.Fatalf("%v: not cut to the month", d)
		}
	}
}

func TestNullRate(t *testing.T) {
	c := genCase{gen: apitypes.GenSequence, col: col("id", "BIGINT"), args: Args{"nullRate": 0.25}}
	nulls := 0
	for _, v := range draw(t, c, 4000) {
		if v == nil {
			nulls++
		}
	}
	if nulls < 900 || nulls > 1100 {
		t.Errorf("nulls = %d of 4000, want about 1000", nulls)
	}
	// Default "source" takes the profile rate.
	c = genCase{gen: apitypes.GenDistribution, col: col("r", "DOUBLE"), p: numberProfile()}
	nulls = 0
	for _, v := range draw(t, c, 4000) {
		if v == nil {
			nulls++
		}
	}
	if nulls < 150 || nulls > 250 {
		t.Errorf("source null rate: %d of 4000, want about 200", nulls)
	}
}

func TestTextLengths(t *testing.T) {
	for _, c := range []genCase{
		{gen: apitypes.GenRandomString, col: col("s", "VARCHAR"), args: Args{"charset": "source"}, p: textProfile()},
		{gen: apitypes.GenRandomText, col: col("t", "VARCHAR"), p: textProfile()},
	} {
		for _, v := range draw(t, c, 500) {
			s := v.(string)
			n := len([]rune(s))
			if c.gen == apitypes.GenRandomString && n != 6 && n != 8 && n != 12 {
				t.Fatalf("%s: length %d not in the histogram", c.gen, n)
			}
			if n > 12 || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
				t.Fatalf("%s: bad value %q", c.gen, s)
			}
		}
	}
	lorem := genCase{gen: apitypes.GenLoremText, col: col("l", "VARCHAR"), args: Args{"words": map[string]any{"min": 2.0, "max": 4.0}, "vocabulary": "de"}}
	for _, v := range draw(t, lorem, 200) {
		s := v.(string)
		w := len(strings.Fields(s))
		if w < 2 || w > 4 || !strings.HasSuffix(s, ".") {
			t.Fatalf("lorem %q", s)
		}
	}
}

func TestCheckTypes(t *testing.T) {
	for _, tc := range []struct {
		gen  apitypes.SyntheticGenerator
		typ  string
		args Args
		ok   bool
	}{
		{apitypes.GenRandomString, "INTEGER", nil, false},
		{apitypes.GenDistribution, "VARCHAR", nil, false},
		{apitypes.GenDistribution, "TIME", nil, true},
		{apitypes.GenNormal, "TIME", nil, false},
		{apitypes.GenBoolean, "VARCHAR", nil, false},
		{apitypes.GenUUID, "UUID", nil, true},
		{apitypes.GenUUID, "INTEGER", nil, false},
		{apitypes.GenFake, "UUID", Args{"kind": "uuid"}, true},
		{apitypes.GenFake, "UUID", Args{"kind": "email"}, false},
		{apitypes.GenDateDistribution, "DATE", Args{"granularity": "hour"}, false},
		{apitypes.GenDateDistribution, "TIMESTAMP", Args{"granularity": "hour"}, true},
		{apitypes.GenCategorical, "INTEGER", Args{"otherLabel": "x"}, false},
		{apitypes.GenCategorical, "UUID", Args{"otherLabel": "x"}, false},
		{apitypes.GenCategorical, "ENUM('a', 'b')", Args{"otherLabel": "x"}, false},
		{apitypes.GenCategorical, "BLOB", nil, false},
		{apitypes.GenConstant, "BLOB", nil, false},
		{apitypes.GenUniformCategory, "INTEGER[]", nil, false},
		{apitypes.GenWeighted, "STRUCT(a INTEGER)", nil, false},
		{apitypes.GenShuffleLabels, "BLOB", nil, false},
		{apitypes.GenNull, "STRUCT(a INTEGER)", nil, true},
		{apitypes.GenSequence, "VARCHAR", nil, true},
		{apitypes.GenSequence, "DOUBLE", nil, false},
	} {
		g, _ := Lookup(tc.gen)
		err := g.Check(col("c", tc.typ), tc.args)
		if (err == nil) != tc.ok {
			t.Errorf("%s on %s %v: err = %v", tc.gen, tc.typ, tc.args, err)
		}
	}
	g, _ := Lookup(apitypes.GenRandomString)
	if err := g.Check(col("c", "INTEGER"), nil); !errors.Is(err, ErrType) {
		t.Errorf("want ErrType, got %v", err)
	}
}

func TestEveryGeneratorRegistered(t *testing.T) {
	names := map[apitypes.SyntheticGenerator]bool{}
	for _, c := range cases() {
		names[c.gen] = true
	}
	// Generators that read other columns of the row need RowCtx.Values,
	// which cases() does not give. relations_test.go covers them.
	names[apitypes.GenTemplate] = true
	names[apitypes.GenDateOffset] = true
	if len(registry) != 28 || len(names) != 28 {
		t.Errorf("registry has %d generators, cases cover %d, want 28", len(registry), len(names))
	}
}
