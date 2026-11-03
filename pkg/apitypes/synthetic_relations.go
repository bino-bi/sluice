// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"errors"
	"fmt"
	"math"
	"slices"
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
	GenForeignKey: true,
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

// tableRefArg is a table name of the shape schema.table.
func tableRefArg(v any) error {
	s, _ := v.(string)
	schema, table, ok := strings.Cut(s, ".")
	if !ok || strings.TrimSpace(schema) == "" || strings.TrimSpace(table) == "" || strings.Contains(table, ".") {
		return errors.New("must be schema.table")
	}
	return nil
}

// maxFanOut bounds foreignKey.fanOut, so the slot math stays small.
const maxFanOut = 100000

// fanOutArg is "source" or {min, max}, integers from 0 to maxFanOut.
func fanOutArg(v any) error {
	if _, ok := v.(float64); ok {
		return fmt.Errorf("must be source or {min, max} with integers from 0 to %d", maxFanOut)
	}
	return lengthArg(0, maxFanOut)(v)
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

// ForeignKey returns the parent table and column of a foreignKey column,
// lower-cased. ok is false for other generators.
func (c SyntheticColumn) ForeignKey() (table, column string, ok bool) {
	if c.Generator != GenForeignKey {
		return "", "", false
	}
	table, _ = c.Args["table"].(string)
	column, _ = c.Args["column"].(string)
	return strings.ToLower(table), strings.ToLower(column), true
}

// checkTableRelations checks the refs between the columns entries of one
// table: no column reads itself or an expression column, the graph has no
// cycle, and privacy.rejectExactMatches names no expression or correlated
// column. A ref
// to a column without an entry is a leaf; the build checks that it exists.
// names are the columns keys, sorted. tables are the lower-case names of
// every table of the spec, for foreignKey.
func checkTableRelations(t SyntheticTable, path string, names []string, tables map[string]bool) (string, string) {
	byLower := map[string]string{}
	for _, n := range names {
		byLower[strings.ToLower(n)] = n
	}
	deps := map[string][]string{}
	for _, name := range names {
		low := strings.ToLower(name)
		if parent, _, ok := t.Columns[name].ForeignKey(); ok {
			switch {
			case parent == strings.ToLower(t.Name):
				return path + ".columns." + name + ".args.table", "names its own table, a foreignKey must point to another table"
			case !tables[parent]:
				return path + ".columns." + name + ".args.table", fmt.Sprintf("names table %s, which is not in spec.synthetic.tables", t.Columns[name].Args["table"])
			}
		}
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
	if t.Privacy != nil {
		for _, c := range t.Privacy.RejectExactMatches {
			if n, ok := byLower[strings.ToLower(c)]; ok && t.Columns[n].Generator == GenExpression {
				return path + ".privacy.rejectExactMatches", fmt.Sprintf("names expression column %s, which is computed after the check", n)
			}
			// A redraw asks for an earlier row again, and the copula only
			// moves forward.
			if n, ok := byLower[strings.ToLower(c)]; ok && t.Columns[n].Generator == GenCorrelated {
				return path + ".privacy.rejectExactMatches", fmt.Sprintf("names correlated column %s, which cannot be drawn again", n)
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

// SyntheticTableOrder returns the table indexes, parents before children,
// list order as tie-break. It fails on an unknown table, a table that
// names itself, or a cycle.
func SyntheticTableOrder(spec *SyntheticSpec) ([]int, error) {
	idx := make(map[string]int, len(spec.Tables))
	for i, t := range spec.Tables {
		idx[strings.ToLower(t.Name)] = i
	}
	parents := make([][]int, len(spec.Tables))
	deps := map[string][]string{}
	for i, t := range spec.Tables {
		cols := make([]string, 0, len(t.Columns))
		for name := range t.Columns {
			cols = append(cols, name)
		}
		sort.Strings(cols)
		for _, name := range cols {
			parent, _, ok := t.Columns[name].ForeignKey()
			if !ok {
				continue
			}
			p, found := idx[parent]
			switch {
			case !found:
				return nil, fmt.Errorf("table %s: column %s names table %s, which is not in spec.synthetic.tables", t.Name, name, t.Columns[name].Args["table"])
			case p == i:
				return nil, fmt.Errorf("table %s: column %s names its own table", t.Name, name)
			}
			if !slices.Contains(parents[i], p) {
				parents[i] = append(parents[i], p)
				low := strings.ToLower(t.Name)
				deps[low] = append(deps[low], parent)
			}
		}
	}
	for _, ds := range deps {
		sort.Strings(ds)
	}
	if cycle := findCycle(deps); len(cycle) > 0 {
		show := make([]string, len(cycle))
		for i, c := range cycle {
			show[i] = spec.Tables[idx[c]].Name
		}
		sort.Strings(show)
		return nil, fmt.Errorf("foreignKey cycle between tables %s", strings.Join(show, ", "))
	}
	order := make([]int, 0, len(spec.Tables))
	placed := make([]bool, len(spec.Tables))
	for len(order) < len(spec.Tables) {
		for i := range spec.Tables {
			if placed[i] || slices.ContainsFunc(parents[i], func(p int) bool { return !placed[p] }) {
				continue
			}
			placed[i] = true
			order = append(order, i)
			break
		}
	}
	return order, nil
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

// maxCorrelated caps the columns of one correlated group.
const maxCorrelated = 8

// withArg is correlated.with: a list of 1 to maxCorrelated-1 column
// names, unique ignoring case.
func withArg(v any) error {
	bad := fmt.Errorf("must be a list of 1 to %d column names", maxCorrelated-1)
	list, ok := v.([]any)
	if !ok || len(list) == 0 || len(list) > maxCorrelated-1 {
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

// checkCorrelated checks the correlated columns of one table: with names
// other correlated columns of the same columns map, and every group (a
// connected component of the undirected with edges) has 2 to
// maxCorrelated columns. names are the columns keys, sorted.
func checkCorrelated(t SyntheticTable, path string, names []string) (string, string) {
	byLower := map[string]string{}
	for _, n := range names {
		byLower[strings.ToLower(n)] = n
	}
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p := parent[x]; p != x {
			parent[x] = find(p)
		}
		return parent[x]
	}
	var members []string
	for _, name := range names {
		if t.Columns[name].Generator == GenCorrelated {
			members = append(members, name)
			parent[name] = name
		}
	}
	for _, name := range members {
		list, _ := t.Columns[name].Args["with"].([]any)
		for _, e := range list {
			w, _ := e.(string)
			field := path + ".columns." + name + ".args.with"
			other, ok := byLower[strings.ToLower(w)]
			switch {
			case strings.EqualFold(w, name):
				return field, "names the column itself"
			case !ok:
				return field, fmt.Sprintf("column %q is not listed under columns", w)
			case t.Columns[other].Generator != GenCorrelated:
				return field, fmt.Sprintf("column %q does not use correlated", w)
			}
			parent[find(name)] = find(other)
		}
	}
	size := map[string]int{}
	for _, name := range members {
		size[find(name)]++
	}
	for _, name := range members {
		switch n := size[find(name)]; {
		case n < 2:
			return path + ".columns." + name, "correlated needs at least one other column"
		case n > maxCorrelated:
			return path + ".columns." + name, fmt.Sprintf("a correlated group has at most %d columns", maxCorrelated)
		}
	}
	return "", ""
}
