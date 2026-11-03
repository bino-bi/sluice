// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// SyntheticSpec configures a DataSource of type synthetic: a snapshot of
// generated rows built from the profile of another data source.
type SyntheticSpec struct {
	Source  string            `yaml:"source" json:"source"`
	Seed    string            `yaml:"seed" json:"seed"`
	Build   SyntheticBuild    `yaml:"build,omitempty" json:"build,omitempty"`
	Privacy *SyntheticPrivacy `yaml:"privacy,omitempty" json:"privacy,omitempty"`
	Rules   []SyntheticRule   `yaml:"rules,omitempty" json:"rules,omitempty"`
	Tables  []SyntheticTable  `yaml:"tables" json:"tables"`
}

// SyntheticBuild says when the server builds a snapshot on its own.
type SyntheticBuild struct {
	OnStart BuildOnStart `yaml:"onStart,omitempty" json:"onStart,omitempty"`
}

// BuildOnStart selects the start-time build behavior.
type BuildOnStart string

const (
	BuildNever     BuildOnStart = "never"
	BuildIfMissing BuildOnStart = "ifMissing"
)

// SyntheticPrivacy holds the privacy filter settings. StrictTags is read
// only at source level.
type SyntheticPrivacy struct {
	MinGroupSize  *int           `yaml:"minGroupSize,omitempty" json:"minGroupSize,omitempty"`
	ClipOutliers  *ClipOutliers  `yaml:"clipOutliers,omitempty" json:"clipOutliers,omitempty"`
	ExtremeJitter *ExtremeJitter `yaml:"extremeJitter,omitempty" json:"extremeJitter,omitempty"`
	SpikeWarning  *bool          `yaml:"spikeWarning,omitempty" json:"spikeWarning,omitempty"`
	StrictTags    []string       `yaml:"strictTags,omitempty" json:"strictTags,omitempty"`
}

// ClipOutliers names the source quantiles that bound number and date
// columns.
type ClipOutliers struct {
	Lower float64 `yaml:"lower" json:"lower"`
	Upper float64 `yaml:"upper" json:"upper"`
}

// ExtremeJitter moves the bounds of a column by a random share of its
// range. YAML `off` (which arrives as false) or "off" turns it off.
type ExtremeJitter struct {
	// No yaml tag: gen-policy-docs skips untagged fields.
	Off bool    `json:"-"`
	Min float64 `yaml:"min" json:"min"`
	Max float64 `yaml:"max" json:"max"`
}

// UnmarshalJSON accepts false, "off", or an object with min and max.
func (e *ExtremeJitter) UnmarshalJSON(data []byte) error {
	switch t := string(bytes.TrimSpace(data)); t {
	case "false", `"off"`:
		*e = ExtremeJitter{Off: true}
		return nil
	case "null":
		return nil
	}
	var v struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return errors.New("extremeJitter: use off or an object with min and max")
	}
	*e = ExtremeJitter{Min: v.Min, Max: v.Max}
	return nil
}

// MarshalJSON writes "off" or the object.
func (e ExtremeJitter) MarshalJSON() ([]byte, error) {
	if e.Off {
		return []byte(`"off"`), nil
	}
	return json.Marshal(struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
	}{e.Min, e.Max})
}

// SyntheticTable is one table of the snapshot. Name is schema.table in
// the source catalog.
type SyntheticTable struct {
	Name    string                     `yaml:"name" json:"name"`
	Size    SyntheticSize              `yaml:"size" json:"size"`
	Privacy *SyntheticPrivacy          `yaml:"privacy,omitempty" json:"privacy,omitempty"`
	Columns map[string]SyntheticColumn `yaml:"columns,omitempty" json:"columns,omitempty"`
	Rules   []SyntheticRule            `yaml:"rules,omitempty" json:"rules,omitempty"`
}

// SyntheticSize sets the row count: a share of the source rows or a fixed
// number. Exactly one is set.
type SyntheticSize struct {
	Ratio *float64 `yaml:"ratio,omitempty" json:"ratio,omitempty"`
	Rows  *int64   `yaml:"rows,omitempty" json:"rows,omitempty"`
}

// SyntheticColumn picks the generator of one column.
type SyntheticColumn struct {
	Generator SyntheticGenerator `yaml:"generator" json:"generator"`
	Args      map[string]any     `yaml:"args,omitempty" json:"args,omitempty"`
	Privacy   *SyntheticPrivacy  `yaml:"privacy,omitempty" json:"privacy,omitempty"`
}

// SyntheticRule picks the generator of every column it matches.
type SyntheticRule struct {
	Match     SyntheticMatch     `yaml:"match" json:"match"`
	Generator SyntheticGenerator `yaml:"generator" json:"generator"`
	Args      map[string]any     `yaml:"args,omitempty" json:"args,omitempty"`
}

// SyntheticMatch selects columns. Fields are ANDed, the entries of one
// field are ORed.
type SyntheticMatch struct {
	Columns []string `yaml:"columns,omitempty" json:"columns,omitempty"`
	Types   []string `yaml:"types,omitempty" json:"types,omitempty"`
	Tags    []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// SyntheticGenerator names a value generator.
type SyntheticGenerator string

const (
	GenDistribution     SyntheticGenerator = "distribution"
	GenUniform          SyntheticGenerator = "uniform"
	GenNormal           SyntheticGenerator = "normal"
	GenLognormal        SyntheticGenerator = "lognormal"
	GenHistogram        SyntheticGenerator = "histogram"
	GenBucket           SyntheticGenerator = "bucket"
	GenConstant         SyntheticGenerator = "constant"
	GenSequence         SyntheticGenerator = "sequence"
	GenRange            SyntheticGenerator = "range"
	GenCategorical      SyntheticGenerator = "categorical"
	GenUniformCategory  SyntheticGenerator = "uniformCategory"
	GenWeighted         SyntheticGenerator = "weighted"
	GenBoolean          SyntheticGenerator = "boolean"
	GenShuffleLabels    SyntheticGenerator = "shuffleLabels"
	GenRandomString     SyntheticGenerator = "randomString"
	GenPattern          SyntheticGenerator = "pattern"
	GenRegex            SyntheticGenerator = "regex"
	GenFake             SyntheticGenerator = "fake"
	GenUUID             SyntheticGenerator = "uuid"
	GenPrefixKeep       SyntheticGenerator = "prefixKeep"
	GenLoremText        SyntheticGenerator = "loremText"
	GenRandomText       SyntheticGenerator = "randomText"
	GenRedact           SyntheticGenerator = "redact"
	GenDateDistribution SyntheticGenerator = "dateDistribution"
	GenDateRange        SyntheticGenerator = "dateRange"
	GenNull             SyntheticGenerator = "null"
	GenTemplate         SyntheticGenerator = "template"
	GenDateOffset       SyntheticGenerator = "dateOffset"
)

// argCheck validates one arg value as it arrives from JSON: numbers are
// float64, lists []any, objects map[string]any.
type argCheck func(v any) error

// genSchema lists the args of a generator. required names the args that
// must be set. cross runs after every single arg passed.
type genSchema struct {
	args     map[string]argCheck
	required []string
	cross    func(args map[string]any) (arg string, err error)
	// noUnique refuses the shared arg unique: a redraw of the same row
	// gives the same value, so the unique retry loop could not end well.
	noUnique bool
}

var syntheticGenerators = map[SyntheticGenerator]genSchema{
	GenDistribution: {args: map[string]argCheck{"decimals": decimalsArg, "integer": boolArg, "smooth": boolArg, "by": byArg}},
	GenUniform:      {args: map[string]argCheck{"min": numberArg, "max": numberArg}, cross: minBelowMax},
	GenNormal:       {args: map[string]argCheck{"truncate": boolArg, "decimals": decimalsArg, "integer": boolArg}},
	GenLognormal:    {args: map[string]argCheck{"truncate": boolArg, "decimals": decimalsArg, "integer": boolArg}},
	GenHistogram:    {args: map[string]argCheck{"bins": intArg(2, 1000), "decimals": decimalsArg, "integer": boolArg}},
	GenBucket: {args: map[string]argCheck{"edges": edgesArg, "buckets": intArg(2, 100)}, cross: func(a map[string]any) (string, error) {
		if _, ok := a["edges"]; ok {
			if _, ok := a["buckets"]; ok {
				return "buckets", errors.New("set edges or buckets, not both")
			}
		}
		return "", nil
	}},
	GenConstant:        {args: map[string]argCheck{"value": scalarArg}, required: []string{"value"}},
	GenSequence:        {args: map[string]argCheck{"start": intArg(-1<<53, 1<<53), "step": stepArg}},
	GenRange:           {args: map[string]argCheck{"min": numberArg, "max": numberArg, "decimals": decimalsArg}, required: []string{"min", "max"}, cross: minBelowMax},
	GenCategorical:     {args: map[string]argCheck{"otherLabel": stringArg, "maxValues": intArg(1, 10000), "by": byArg}},
	GenUniformCategory: {args: map[string]argCheck{}},
	GenWeighted:        {args: map[string]argCheck{"values": weightsArg}, required: []string{"values"}},
	GenBoolean:         {args: map[string]argCheck{"trueRate": rateArg}},
	GenShuffleLabels:   {args: map[string]argCheck{"maxValues": intArg(1, 10000)}},
	GenRandomString: {args: map[string]argCheck{
		"length":  lengthArg(1, 1000),
		"initial": initialArg,
		"charset": enumArg("letters", "alnum", "source"),
		"case":    enumArg("source", "lower", "upper", "title"),
	}},
	GenPattern: {args: map[string]argCheck{"maxPatterns": intArg(1, 1000)}},
	GenRegex:   {args: map[string]argCheck{"pattern": regexArg}, required: []string{"pattern"}},
	GenFake: {args: map[string]argCheck{
		"kind":        enumArg("name", "first_name", "last_name", "email", "phone", "company", "city", "country", "street", "iban", "uuid"),
		"locale":      enumArg("en"),
		"keepInitial": boolArg,
	}, required: []string{"kind"}, cross: func(a map[string]any) (string, error) {
		if keep, _ := a["keepInitial"].(bool); keep {
			switch a["kind"] {
			case "name", "first_name", "last_name", "city", "company", "street":
			default:
				return "keepInitial", errors.New("only for kind name, first_name, last_name, city, company or street")
			}
		}
		return "", nil
	}},
	GenUUID:             {args: map[string]argCheck{"version": versionArg}},
	GenPrefixKeep:       {args: map[string]argCheck{"prefixLength": intArg(1, 32)}, required: []string{"prefixLength"}},
	GenLoremText:        {args: map[string]argCheck{"words": lengthArg(1, 10000), "vocabulary": enumArg("lorem", "en", "de")}},
	GenRandomText:       {args: map[string]argCheck{"length": lengthArg(1, 100000), "vocabulary": enumArg("lorem")}},
	GenRedact:           {args: map[string]argCheck{"value": stringArg}},
	GenDateDistribution: {args: map[string]argCheck{"granularity": granularityArg, "keepWeekday": boolArg, "keepSeasonality": boolArg, "by": byArg}},
	GenDateRange:        {args: map[string]argCheck{"min": dateArg, "max": dateArg, "granularity": granularityArg}, cross: dateMinBelowMax},
	GenNull:             {args: map[string]argCheck{}},
	GenTemplate:         {args: map[string]argCheck{"template": templateArg, "lower": boolArg}, required: []string{"template"}, noUnique: true},
	GenDateOffset:       {args: map[string]argCheck{"from": columnArg, "offset": offsetArg}, required: []string{"from"}},
}

// sharedArgs are valid on every generator.
var sharedArgs = map[string]argCheck{"nullRate": rateArg, "unique": boolArg}

var granularityArg = enumArg("second", "minute", "hour", "day", "month", "year")

// syntheticName is the name rule of a synthetic data source: it must be
// both an RFC 1123 label and a DuckDB identifier without quoting.
var syntheticName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// validateSynthetic checks spec.synthetic of a DataSource of type
// synthetic. Existence of the source and the seed provider are checked in
// internal/config, after all files are loaded.
func validateSynthetic(d *DataSource) error {
	fail := func(field, reason string) error {
		return &ValidationError{Kind: d.GetKind(), Name: d.Metadata.Name, Field: field, Reason: reason}
	}
	s := d.Spec
	if !syntheticName.MatchString(d.Metadata.Name) {
		return fail("metadata.name", "a synthetic data source name has only lower-case letters and digits and starts with a letter")
	}
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"path", s.Path != ""},
		{"connection", s.Connection != ""},
		{"credentialsRef", s.CredentialsRef != ""},
		{"bucket", s.Bucket != ""},
		{"region", s.Region != ""},
		{"allowedPaths", len(s.AllowedPaths) > 0},
		{"endpoint", s.Endpoint != ""},
		{"urls", len(s.URLs) > 0},
		{"headersRef", s.HeadersRef != ""},
		{"format", s.Format != ""},
		{"database", s.Database != ""},
		{"tokenRef", s.TokenRef != ""},
	} {
		if f.set {
			return fail("spec."+f.name, "not used by synthetic")
		}
	}
	syn := s.Synthetic
	if syn == nil {
		return fail("spec.synthetic", "required for synthetic")
	}
	const base = "spec.synthetic"
	if syn.Source == "" {
		return fail(base+".source", "required")
	}
	if !rfc1123Label.MatchString(syn.Source) {
		return fail(base+".source", "must be the name of a DataSource")
	}
	if syn.Seed == "" {
		return fail(base+".seed", "required")
	}
	if !strings.HasPrefix(syn.Seed, "secret://") {
		return fail(base+".seed", "must be a secret reference (secret://...)")
	}
	switch syn.Build.OnStart {
	case "", BuildNever, BuildIfMissing:
	default:
		return fail(base+".build.onStart", fmt.Sprintf("unknown value %q (never, ifMissing)", syn.Build.OnStart))
	}
	if field, reason := checkPrivacy(syn.Privacy, base+".privacy", true); field != "" {
		return fail(field, reason)
	}
	for i, r := range syn.Rules {
		if field, reason := checkRule(r, fmt.Sprintf("%s.rules[%d]", base, i)); field != "" {
			return fail(field, reason)
		}
	}
	if len(syn.Tables) == 0 {
		return fail(base+".tables", "at least one table is required")
	}
	names := map[string]bool{}
	for i, t := range syn.Tables {
		path := fmt.Sprintf("%s.tables[%d]", base, i)
		schema, table, ok := strings.Cut(t.Name, ".")
		if !ok || schema == "" || table == "" || strings.Contains(table, ".") {
			return fail(path+".name", "must be schema.table")
		}
		key := strings.ToLower(t.Name)
		if names[key] {
			return fail(path+".name", fmt.Sprintf("table %q is listed twice", t.Name))
		}
		names[key] = true
		if field, reason := checkSize(t.Size, path+".size"); field != "" {
			return fail(field, reason)
		}
		if field, reason := checkPrivacy(t.Privacy, path+".privacy", false); field != "" {
			return fail(field, reason)
		}
		cols := make([]string, 0, len(t.Columns))
		for name := range t.Columns {
			cols = append(cols, name)
		}
		sort.Strings(cols)
		seen := map[string]string{}
		for _, name := range cols {
			cpath := path + ".columns." + name
			if strings.TrimSpace(name) == "" {
				return fail(path+".columns", "column name is empty")
			}
			if other, dup := seen[strings.ToLower(name)]; dup {
				return fail(cpath, fmt.Sprintf("same column as %q", other))
			}
			seen[strings.ToLower(name)] = name
			c := t.Columns[name]
			if field, reason := checkGenerator(c.Generator, c.Args, cpath); field != "" {
				return fail(field, reason)
			}
			if field, reason := checkPrivacy(c.Privacy, cpath+".privacy", false); field != "" {
				return fail(field, reason)
			}
		}
		if field, reason := checkTableRelations(t, path, cols); field != "" {
			return fail(field, reason)
		}
		for j, r := range t.Rules {
			if field, reason := checkRule(r, fmt.Sprintf("%s.rules[%d]", path, j)); field != "" {
				return fail(field, reason)
			}
		}
	}
	return nil
}

func checkSize(s SyntheticSize, path string) (string, string) {
	switch {
	case (s.Ratio == nil) == (s.Rows == nil):
		return path, "set exactly one of ratio or rows"
	case s.Ratio != nil && (*s.Ratio <= 0 || *s.Ratio > 1):
		return path + ".ratio", "must be above 0 and at most 1"
	case s.Rows != nil && *s.Rows < 0:
		return path + ".rows", "must be 0 or more"
	}
	return "", ""
}

func checkPrivacy(p *SyntheticPrivacy, path string, sourceLevel bool) (string, string) {
	if p == nil {
		return "", ""
	}
	if p.MinGroupSize != nil && *p.MinGroupSize < 2 {
		return path + ".minGroupSize", "must be 2 or more"
	}
	if c := p.ClipOutliers; c != nil && (c.Lower < 0 || c.Lower >= c.Upper || c.Upper > 1) {
		return path + ".clipOutliers", "needs 0 <= lower < upper <= 1"
	}
	if j := p.ExtremeJitter; j != nil && !j.Off && (j.Min < 0 || j.Min > j.Max || j.Max > 0.5) {
		return path + ".extremeJitter", "needs 0 <= min <= max <= 0.5, or off"
	}
	if len(p.StrictTags) > 0 {
		if !sourceLevel {
			return path + ".strictTags", "only allowed in spec.synthetic.privacy"
		}
		for _, tag := range p.StrictTags {
			if !tagPattern.MatchString(tag) {
				return path + ".strictTags", fmt.Sprintf("tag %q invalid (use [a-z0-9._-])", tag)
			}
		}
	}
	return "", ""
}

func checkRule(r SyntheticRule, path string) (string, string) {
	m := r.Match
	if len(m.Columns) == 0 && len(m.Types) == 0 && len(m.Tags) == 0 {
		return path + ".match", "needs columns, types or tags"
	}
	for _, f := range []struct {
		name  string
		globs []string
	}{{"columns", m.Columns}, {"types", m.Types}} {
		for _, g := range f.globs {
			if _, err := CompileWildcard(g); err != nil {
				return path + ".match." + f.name, fmt.Sprintf("pattern %q is not valid", g)
			}
		}
	}
	if relationGenerators[r.Generator] {
		return path + ".generator", fmt.Sprintf("%s refers to other columns, set it under tables[].columns", r.Generator)
	}
	if _, ok := r.Args["by"]; ok {
		return path + ".args.by", "by refers to other columns, set it under tables[].columns"
	}
	return checkGenerator(r.Generator, r.Args, path)
}

func checkGenerator(g SyntheticGenerator, args map[string]any, path string) (string, string) {
	if g == "" {
		return path + ".generator", "required"
	}
	schema, ok := syntheticGenerators[g]
	if !ok {
		return path + ".generator", fmt.Sprintf("unknown generator %q", g)
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		check, ok := schema.args[k]
		if !ok {
			check, ok = sharedArgs[k]
		}
		if !ok {
			return path + ".args." + k, fmt.Sprintf("unknown arg for %s", g)
		}
		if err := check(args[k]); err != nil {
			return path + ".args." + k, err.Error()
		}
		if k == "unique" && schema.noUnique {
			return path + ".args." + k, fmt.Sprintf("not allowed for %s", g)
		}
	}
	for _, k := range schema.required {
		if _, ok := args[k]; !ok {
			return path + ".args." + k, "required"
		}
	}
	if schema.cross != nil {
		if arg, err := schema.cross(args); err != nil {
			return path + ".args." + arg, err.Error()
		}
	}
	return "", ""
}

func boolArg(v any) error {
	if _, ok := v.(bool); !ok {
		return errors.New("must be true or false")
	}
	return nil
}

func stringArg(v any) error {
	if _, ok := v.(string); !ok {
		return errors.New("must be a string")
	}
	return nil
}

func numberArg(v any) error {
	if _, ok := v.(float64); !ok {
		return errors.New("must be a number")
	}
	return nil
}

func scalarArg(v any) error {
	switch v.(type) {
	case string, float64, bool:
		return nil
	}
	return errors.New("must be a string, number or boolean")
}

func intArg(lo, hi int64) argCheck {
	return func(v any) error {
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
			return fmt.Errorf("must be an integer from %d to %d", lo, hi)
		}
		return nil
	}
}

var decimalsArg = intArg(0, 18)

func stepArg(v any) error {
	if err := intArg(-1<<53, 1<<53)(v); err != nil {
		return err
	}
	if v.(float64) == 0 {
		return errors.New("must not be 0")
	}
	return nil
}

func versionArg(v any) error {
	if f, ok := v.(float64); ok && (f == 4 || f == 7) {
		return nil
	}
	return errors.New("must be 4 or 7")
}

// rateArg is "source" or a number from 0 to 1.
func rateArg(v any) error {
	if s, ok := v.(string); ok && s == "source" {
		return nil
	}
	if f, ok := v.(float64); ok && f >= 0 && f <= 1 {
		return nil
	}
	return errors.New("must be source or a number from 0 to 1")
}

func enumArg(values ...string) argCheck {
	return func(v any) error {
		if s, ok := v.(string); ok {
			for _, want := range values {
				if s == want {
					return nil
				}
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(values, ", "))
	}
}

// lengthArg is "source", an integer in [lo, hi], or {min, max} with both
// in [lo, hi] and min <= max.
func lengthArg(lo, hi int64) argCheck {
	return func(v any) error {
		bad := fmt.Errorf("must be source, an integer from %d to %d, or {min, max}", lo, hi)
		switch x := v.(type) {
		case string:
			if x == "source" {
				return nil
			}
		case float64:
			if intArg(lo, hi)(x) == nil {
				return nil
			}
		case map[string]any:
			mn, okMin := x["min"]
			mx, okMax := x["max"]
			if len(x) != 2 || !okMin || !okMax || intArg(lo, hi)(mn) != nil || intArg(lo, hi)(mx) != nil {
				return bad
			}
			if mn.(float64) > mx.(float64) {
				return errors.New("min must not be above max")
			}
			return nil
		}
		return bad
	}
}

func initialArg(v any) error {
	if s, ok := v.(string); ok && (s == "source" || s == "random" || utf8.RuneCountInString(s) == 1) {
		return nil
	}
	return errors.New("must be source, random, or one character")
}

func edgesArg(v any) error {
	list, ok := v.([]any)
	if !ok || len(list) < 2 || len(list) > 100 {
		return errors.New("must be a list of 2 to 100 numbers")
	}
	prev := math.Inf(-1)
	for _, e := range list {
		f, ok := e.(float64)
		if !ok {
			return errors.New("must be a list of 2 to 100 numbers")
		}
		if f <= prev {
			return errors.New("must be sorted, without repeats")
		}
		prev = f
	}
	return nil
}

func weightsArg(v any) error {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 || len(m) > 10000 {
		return errors.New("must map 1 to 10000 values to weights")
	}
	for _, w := range m {
		if f, ok := w.(float64); !ok || f <= 0 {
			return errors.New("every weight must be a number above 0")
		}
	}
	return nil
}

func minBelowMax(a map[string]any) (string, error) {
	mn, okMin := a["min"].(float64)
	mx, okMax := a["max"].(float64)
	if okMin && okMax && mn >= mx {
		return "max", errors.New("must be above min")
	}
	return "", nil
}

var dateLayouts = []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05", time.RFC3339Nano}

func parseDateArg(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func dateArg(v any) error {
	if _, ok := parseDateArg(v); !ok {
		return errors.New("must be an ISO date or timestamp")
	}
	return nil
}

func dateMinBelowMax(a map[string]any) (string, error) {
	mn, okMin := parseDateArg(a["min"])
	mx, okMax := parseDateArg(a["max"])
	if okMin && okMax && !mn.Before(mx) {
		return "max", errors.New("must be after min")
	}
	return "", nil
}

// maxRegexOutput caps the longest string a regex generator can produce.
const maxRegexOutput = 1000

func regexArg(v any) error {
	s, ok := v.(string)
	if !ok || s == "" {
		return errors.New("must be a non-empty string")
	}
	if len(s) > 256 {
		return errors.New("must be at most 256 bytes")
	}
	re, err := syntax.Parse(s, syntax.Perl)
	if err != nil {
		var se *syntax.Error
		if errors.As(err, &se) {
			return fmt.Errorf("not a valid regular expression: %s", se.Code)
		}
		return errors.New("not a valid regular expression")
	}
	if !producesChars(re) {
		return errors.New("matches only empty text")
	}
	if regexMaxLen(re) > maxRegexOutput {
		return fmt.Errorf("can produce more than %d characters", maxRegexOutput)
	}
	return nil
}

// producesChars reports whether re has any op that emits a character.
func producesChars(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune) > 0
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return true
	}
	for _, sub := range re.Sub {
		if producesChars(sub) {
			return true
		}
	}
	return false
}

// regexMaxLen is the longest output of the generator walk, computed on
// the parsed tree (Simplify would expand repeats). Open repeats are capped
// at min + 8, as the generator does. The result saturates above
// maxRegexOutput so nested repeats cannot overflow.
func regexMaxLen(re *syntax.Regexp) int {
	sat := func(n int) int { return min(n, maxRegexOutput+1) }
	switch re.Op {
	case syntax.OpLiteral:
		return sat(len(re.Rune))
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpCapture:
		return regexMaxLen(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, sub := range re.Sub {
			n = sat(n + regexMaxLen(sub))
		}
		return n
	case syntax.OpAlternate:
		n := 0
		for _, sub := range re.Sub {
			n = max(n, regexMaxLen(sub))
		}
		return n
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		hi := 1
		switch re.Op {
		case syntax.OpStar:
			hi = 8
		case syntax.OpPlus:
			hi = 9
		case syntax.OpRepeat:
			hi = re.Max
			if hi < 0 {
				hi = re.Min + 8
			}
		}
		return sat(hi * regexMaxLen(re.Sub[0]))
	}
	return 0
}
