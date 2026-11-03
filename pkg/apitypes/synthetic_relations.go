// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// relationGenerators read other columns of the same row. A rule matches
// columns of many tables, where a column name would point elsewhere or
// nowhere, so they are only allowed under tables[].columns.
var relationGenerators = map[SyntheticGenerator]bool{
	GenTemplate:   true,
	GenDateOffset: true,
	GenExpression: true,
}

// maxTemplateBytes caps the template arg.
const maxTemplateBytes = 1024

func templateArg(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("must be a string")
	}
	if len(s) > maxTemplateBytes {
		return fmt.Errorf("must be at most %d bytes", maxTemplateBytes)
	}
	_, _, err := ParseTemplate(s)
	return err
}

// maxSQLBytes caps the expression arg sql.
const maxSQLBytes = 1024

// sqlArg is the shape check of expression.sql. The builder parses it with
// DuckDB at build start; a comment or a line break could change the
// meaning of the statement the text is embedded in.
func sqlArg(v any) error {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return errors.New("must be a non-empty string")
	}
	if len(s) > maxSQLBytes {
		return fmt.Errorf("must be at most %d bytes", maxSQLBytes)
	}
	switch {
	case strings.ContainsAny(s, ";\x00"):
		return errors.New("must not hold ; or a NUL byte")
	case strings.Contains(s, "--"), strings.Contains(s, "/*"):
		return errors.New("must not hold a comment (-- or /*)")
	case strings.ContainsAny(s, "\r\n"):
		return errors.New("must be one line")
	}
	return nil
}

// columnArg is the name of another column of the same table.
func columnArg(v any) error {
	if s, ok := v.(string); !ok || strings.TrimSpace(s) == "" {
		return errors.New("must be a column name")
	}
	return nil
}

// maxByColumns caps the by arg; every column multiplies the groups.
const maxByColumns = 3

// byArg is a list of 1 to 3 column names, unique ignoring case.
func byArg(v any) error {
	bad := fmt.Errorf("must be a list of 1 to %d column names", maxByColumns)
	list, ok := v.([]any)
	if !ok || len(list) == 0 || len(list) > maxByColumns {
		return bad
	}
	seen := map[string]bool{}
	for _, e := range list {
		s, ok := e.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return bad
		}
		if seen[strings.ToLower(s)] {
			return fmt.Errorf("names column %s twice", s)
		}
		seen[strings.ToLower(s)] = true
	}
	return nil
}

// maxOffsetDays bounds dateOffset.offset, about 100 years.
const maxOffsetDays = 36500

// offsetArg is "source" or {min, max} in days, min <= max.
func offsetArg(v any) error {
	bad := fmt.Errorf("must be source or {min, max} in days from %d to %d", -maxOffsetDays, maxOffsetDays)
	switch x := v.(type) {
	case string:
		if x == "source" {
			return nil
		}
	case map[string]any:
		mn, okMin := x["min"].(float64)
		mx, okMax := x["max"].(float64)
		if len(x) != 2 || !okMin || !okMax || math.Abs(mn) > maxOffsetDays || math.Abs(mx) > maxOffsetDays {
			return bad
		}
		if mn > mx {
			return errors.New("min must not be above max")
		}
		return nil
	}
	return bad
}

// ParseTemplate splits a template into literal text and placeholders.
// {name} is a placeholder, {{ and }} are literal braces. lits has one
// more entry than refs: lits[0] refs[0] lits[1] ... lits[n]. refs are
// the names as written. A template needs at least one placeholder.
func ParseTemplate(tmpl string) (lits, refs []string, err error) {
	var lit strings.Builder
	for i := 0; i < len(tmpl); i++ {
		switch c := tmpl[i]; c {
		case '{':
			if i+1 < len(tmpl) && tmpl[i+1] == '{' {
				lit.WriteByte('{')
				i++
				continue
			}
			end := strings.IndexAny(tmpl[i+1:], "{}")
			if end < 0 || tmpl[i+1+end] != '}' {
				return nil, nil, errors.New("has a { without a closing }")
			}
			name := tmpl[i+1 : i+1+end]
			if strings.TrimSpace(name) == "" {
				return nil, nil, errors.New("has an empty placeholder {}")
			}
			lits = append(lits, lit.String())
			lit.Reset()
			refs = append(refs, name)
			i += end + 1
		case '}':
			if i+1 < len(tmpl) && tmpl[i+1] == '}' {
				lit.WriteByte('}')
				i++
				continue
			}
			return nil, nil, errors.New("has a } without an opening { (use }} for a literal brace)")
		default:
			lit.WriteByte(c)
		}
	}
	if len(refs) == 0 {
		return nil, nil, errors.New("has no {column} placeholder, use constant for a fixed text")
	}
	return append(lits, lit.String()), refs, nil
}

// TemplateRefs returns the placeholder names of a template in order of
// appearance, as written.
func TemplateRefs(tmpl string) ([]string, error) {
	_, refs, err := ParseTemplate(tmpl)
	return refs, err
}

// argRefs returns, per arg, the columns of the same table that c reads,
// lower-cased. Args that do not parse give no refs; decode refuses them.
func (c SyntheticColumn) argRefs() map[string][]string {
	out := map[string][]string{}
	switch c.Generator {
	case GenTemplate:
		if s, ok := c.Args["template"].(string); ok {
			if refs, err := TemplateRefs(s); err == nil {
				for _, r := range refs {
					out["template"] = append(out["template"], strings.ToLower(r))
				}
			}
		}
	case GenDateOffset:
		if s, ok := c.Args["from"].(string); ok && strings.TrimSpace(s) != "" {
			out["from"] = []string{strings.ToLower(s)}
		}
	case GenDistribution, GenCategorical, GenDateDistribution:
		list, _ := c.Args["by"].([]any)
		for _, e := range list {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out["by"] = append(out["by"], strings.ToLower(s))
			}
		}
	}
	return out
}

// Refs returns the columns of the same table that c reads: template
// placeholders, dateOffset.from and by, lower-cased, sorted, unique.
func (c SyntheticColumn) Refs() []string {
	seen := map[string]bool{}
	var out []string
	for _, refs := range c.argRefs() {
		for _, r := range refs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}

// checkTableRelations checks the refs between the columns entries of one
// table: no column reads itself or an expression column, and the graph
// has no cycle. A ref to a
// column without an entry is a leaf; the build checks that it exists.
// names are the columns keys, sorted.
func checkTableRelations(t SyntheticTable, path string, names []string) (string, string) {
	byLower := map[string]string{}
	for _, n := range names {
		byLower[strings.ToLower(n)] = n
	}
	deps := map[string][]string{}
	for _, name := range names {
		low := strings.ToLower(name)
		args := t.Columns[name].argRefs()
		argNames := make([]string, 0, len(args))
		for a := range args {
			argNames = append(argNames, a)
		}
		sort.Strings(argNames)
		for _, a := range argNames {
			for _, r := range args[a] {
				if r == low {
					return path + ".columns." + name + ".args." + a, "refers to the column itself"
				}
				if n, ok := byLower[r]; ok && t.Columns[n].Generator == GenExpression {
					return path + ".columns." + name + ".args." + a, fmt.Sprintf("names expression column %s, which is computed after the other columns", n)
				}
			}
		}
		for _, r := range t.Columns[name].Refs() {
			if _, ok := byLower[r]; ok {
				deps[low] = append(deps[low], r)
			}
		}
	}
	if cycle := findCycle(deps); len(cycle) > 0 {
		show := make([]string, len(cycle))
		for i, c := range cycle {
			show[i] = byLower[c]
		}
		sort.Strings(show)
		return path + ".columns", fmt.Sprintf("columns %s depend on each other", strings.Join(show, ", "))
	}
	return "", ""
}

// findCycle returns the nodes of one cycle of the graph (node -> the
// nodes it reads), or nil. deps lists are sorted, so the result is
// stable.
func findCycle(deps map[string][]string) []string {
	done := map[string]bool{}
	for {
		progress := false
		for n, ds := range deps {
			if done[n] {
				continue
			}
			ready := true
			for _, d := range ds {
				if _, isNode := deps[d]; isNode && !done[d] {
					ready = false
					break
				}
			}
			if ready {
				done[n] = true
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	var left []string
	for n := range deps {
		if !done[n] {
			left = append(left, n)
		}
	}
	if len(left) == 0 {
		return nil
	}
	sort.Strings(left)
	// Every node left reads at least one other node left, so a walk along
	// the smallest such dep must repeat a node. The repeated part is a cycle.
	pos := map[string]int{}
	var walk []string
	n := left[0]
	for {
		if i, ok := pos[n]; ok {
			return walk[i:]
		}
		pos[n] = len(walk)
		walk = append(walk, n)
		for _, d := range deps[n] {
			if _, isNode := deps[d]; isNode && !done[d] {
				n = d
				break
			}
		}
	}
}
