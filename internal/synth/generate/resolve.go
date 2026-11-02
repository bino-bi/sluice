// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Choice is the generator a column gets.
type Choice struct {
	Generator apitypes.SyntheticGenerator
	Args      Args
	// ChosenBy is column, table-rule[i], source-rule[i] or default.
	ChosenBy string
	// Privacy is the privacy of the columns entry, nil otherwise.
	Privacy *apitypes.SyntheticPrivacy
	// Fallback is set for every profile-based default, because a default
	// is not a choice and must not fail the build. The builder uses it
	// when the profile or New fails, and for the VARCHAR default
	// categorical also when the top values have Rest > 0. A chain ends
	// in null with a Warning.
	Fallback *Choice
	// Warning is set when the default writes NULL.
	Warning string
}

// DefaultStrictTags applies when spec.synthetic.privacy.strictTags is not
// set.
var DefaultStrictTags = []string{"pii"}

// maxDefaultCategories is the most distinct values a VARCHAR column can
// have to default to categorical (concept 4.6).
const maxDefaultCategories = 50

type rule struct {
	columns, types []apitypes.Matcher
	tags           []string
	generator      apitypes.SyntheticGenerator
	args           Args
}

// Resolver picks the generator of each column of one table (plan 1.10):
// tables[].columns, then tables[].rules, then synthetic.rules, then the
// default of the type.
type Resolver struct {
	strict  []string
	k       int
	columns map[string]apitypes.SyntheticColumn
	// names maps a lower-case columns key to the key as written.
	names       map[string]string
	tableRules  []rule
	sourceRules []rule
}

// NewResolver compiles the choices of one table.
func NewResolver(spec *apitypes.SyntheticSpec, table *apitypes.SyntheticTable) (*Resolver, error) {
	r := &Resolver{
		strict:  DefaultStrictTags,
		k:       profile.Effective(table.Privacy, spec.Privacy).MinGroupSize,
		columns: map[string]apitypes.SyntheticColumn{},
		names:   map[string]string{},
	}
	if spec.Privacy != nil && len(spec.Privacy.StrictTags) > 0 {
		r.strict = spec.Privacy.StrictTags
	}
	for name, c := range table.Columns {
		r.columns[strings.ToLower(name)] = c
		r.names[strings.ToLower(name)] = name
	}
	var err error
	if r.tableRules, err = compileRules(table.Rules); err != nil {
		return nil, err
	}
	if r.sourceRules, err = compileRules(spec.Rules); err != nil {
		return nil, err
	}
	return r, nil
}

func compileRules(in []apitypes.SyntheticRule) ([]rule, error) {
	out := make([]rule, len(in))
	for i, sr := range in {
		out[i] = rule{tags: sr.Match.Tags, generator: sr.Generator, args: sr.Args}
		for _, g := range sr.Match.Columns {
			m, err := apitypes.CompileWildcard(strings.ToLower(g))
			if err != nil {
				return nil, err
			}
			out[i].columns = append(out[i].columns, m)
		}
		for _, g := range sr.Match.Types {
			m, err := apitypes.CompileWildcard(strings.ToUpper(g))
			if err != nil {
				return nil, err
			}
			out[i].types = append(out[i].types, m)
		}
	}
	return out, nil
}

func matchAny(ms []apitypes.Matcher, s string) bool {
	for _, m := range ms {
		if m.Match(s) {
			return true
		}
	}
	return false
}

func (r rule) matches(col Column, tags []string) bool {
	if len(r.columns) > 0 && !matchAny(r.columns, strings.ToLower(col.Name)) {
		return false
	}
	if len(r.types) > 0 && !matchAny(r.types, strings.ToUpper(strings.TrimSpace(col.Type))) {
		return false
	}
	if len(r.tags) > 0 && !slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(r.tags, t) }) {
		return false
	}
	return true
}

// Resolve returns the choice of one column. tags are the column's
// DataClassification tags on the source. A strict tag on a column that
// would get the default, and a generator that does not fit the type,
// are errors that name the column.
func (r *Resolver) Resolve(col Column, counts profile.Counts, tags []string) (Choice, error) {
	c, ok := r.explicit(col, tags)
	if !ok {
		for _, t := range tags {
			if slices.Contains(r.strict, t) {
				return Choice{}, fmt.Errorf("column %s: tag %s needs a generator chosen under columns or by a rule", col.Name, t)
			}
		}
		c = r.byType(col, counts)
	}
	g, ok := Lookup(c.Generator)
	if !ok {
		return Choice{}, fmt.Errorf("column %s: unknown generator %q", col.Name, c.Generator)
	}
	if err := g.Check(col, c.Args); err != nil {
		return Choice{}, fmt.Errorf("column %s: %w", col.Name, err)
	}
	return c, nil
}

func (r *Resolver) explicit(col Column, tags []string) (Choice, bool) {
	if sc, ok := r.columns[strings.ToLower(col.Name)]; ok {
		return Choice{Generator: sc.Generator, Args: sc.Args, ChosenBy: "column", Privacy: sc.Privacy}, true
	}
	for _, set := range []struct {
		name  string
		rules []rule
	}{{"table-rule", r.tableRules}, {"source-rule", r.sourceRules}} {
		for i, ru := range set.rules {
			if ru.matches(col, tags) {
				return Choice{Generator: ru.generator, Args: ru.args, ChosenBy: fmt.Sprintf("%s[%d]", set.name, i)}, true
			}
		}
	}
	return Choice{}, false
}

// byType is the default of concept 4.6 (plan 3.4).
func (r *Resolver) byType(col Column, counts profile.Counts) Choice {
	def := func(g apitypes.SyntheticGenerator, args Args) Choice {
		return Choice{Generator: g, Args: args, ChosenBy: "default"}
	}
	kind := col.Kind()
	if kind == profile.KindOther {
		c := def(apitypes.GenNull, nil)
		c.Warning = "type " + col.Type + ", written as NULL"
		return c
	}
	// Every profile-based default fails on fewer than k non-null rows,
	// so the profile query is skipped.
	if counts.NonNull < int64(r.k) {
		c := def(apitypes.GenNull, nil)
		c.Warning = "fewer than minGroupSize non-null rows, written as NULL"
		return c
	}
	// The grid, the groups or the case styles can still fail above k.
	withNull := func(g apitypes.SyntheticGenerator, args Args) Choice {
		null := def(apitypes.GenNull, nil)
		null.Warning = "too few rows for a profile, written as NULL"
		c := def(g, args)
		c.Fallback = &null
		return c
	}
	switch kind {
	case profile.KindInteger, profile.KindFloat, profile.KindTime:
		return withNull(apitypes.GenDistribution, nil)
	case profile.KindBool:
		return withNull(apitypes.GenBoolean, nil)
	case profile.KindEnum:
		return withNull(apitypes.GenCategorical, nil)
	case profile.KindDate, profile.KindTimestamp:
		return withNull(apitypes.GenDateDistribution, nil)
	case profile.KindUUID:
		return def(apitypes.GenUUID, nil)
	}
	str := withNull(apitypes.GenRandomString, Args{"length": "source"})
	if counts.Distinct <= maxDefaultCategories {
		c := def(apitypes.GenCategorical, nil)
		c.Fallback = &str
		return c
	}
	return str
}

// MissingColumns returns the columns entries that name no column of
// names, sorted.
func (r *Resolver) MissingColumns(names []string) []string {
	have := map[string]bool{}
	for _, n := range names {
		have[strings.ToLower(n)] = true
	}
	var out []string
	for k, name := range r.names {
		if !have[k] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
