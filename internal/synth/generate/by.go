// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// BySiblings returns the by columns of args in arg order, or nil when
// args has no by.
func BySiblings(col Column, args Args) ([]Sibling, error) {
	list, _ := args["by"].([]any)
	out := make([]Sibling, 0, len(list))
	for _, e := range list {
		name, _ := e.(string)
		s, ok := col.Siblings[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("by names column %s, which is not in the table", name)
		}
		out = append(out, s)
	}
	return out, nil
}

// checkBy refuses by columns whose values cannot be matched as text with
// the source groups. HUGEINT, UBIGINT and UHUGEINT stage as DOUBLE, whose
// Go text differs from the DuckDB text.
func checkBy(col Column, args Args) error {
	by, err := BySiblings(col, args)
	if err != nil {
		return err
	}
	for _, s := range by {
		sc := Column{Type: s.Type}
		switch sc.Kind() {
		case profile.KindText, profile.KindEnum, profile.KindBool, profile.KindInteger, profile.KindDate:
			if st := StageOf(sc, ""); st != StageDouble {
				continue
			}
		}
		return fmt.Errorf("by column %s has type %s, which cannot be matched", s.Name, profile.DisplayType(s.Type))
	}
	return nil
}

// ByCounter is a ColumnGen with by. It counts the draws that used the
// global profile, because the row's by values named no kept group.
type ByCounter interface {
	ByRows() (fallback, total int64)
}

// NewBy is New for a generator with by: groups maps a group key to its
// profile; rows whose key is not in groups use global. The global and
// per-group generators are built with the inner g.New (no wrapper each).
// nullRate (from global) and unique are applied once, by one wrapped
// around the byGen, so there is one null stream draw per row and one
// seen map across all groups.
func NewBy(name apitypes.SyntheticGenerator, col Column, global *profile.Column,
	groups []profile.Group, args Args, s Streams) (ColumnGen, error) {
	g, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("column %s: unknown generator %q", col.Name, name)
	}
	by, err := BySiblings(col, args)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", col.Name, err)
	}
	if global == nil {
		global = &profile.Column{}
	}
	inner, err := g.New(col, global, args, s)
	if err != nil {
		return nil, fmt.Errorf("column %s: %w", col.Name, err)
	}
	bg := &byGen{global: inner, groups: make(map[string]ColumnGen, len(groups))}
	for _, b := range by {
		bg.idx = append(bg.idx, b.Index)
		bg.types = append(bg.types, b.Type)
	}
	for _, gr := range groups {
		gi, err := g.New(col, gr.Profile, args, s)
		if err != nil {
			return nil, fmt.Errorf("column %s: by-group: %w", col.Name, err)
		}
		bg.groups[gr.Key] = gi
	}
	return wrap(bg, col, global.NullRate, args, s)
}

// byGen draws from the generator of the row's by-group. All inner
// generators share the column's streams; rows are drawn in a fixed
// order, so the result is deterministic.
type byGen struct {
	global          ColumnGen
	groups          map[string]ColumnGen
	idx             []int
	types           []string
	fallback, total int64
}

// Pool is the sum of the inner pools, an upper bound of their union, or
// -1 when one is unknown.
func (g *byGen) Pool() int64 {
	sum := g.global.Pool()
	for _, gen := range g.groups {
		p := gen.Pool()
		if sum < 0 || p < 0 || p > math.MaxInt64-sum {
			return -1
		}
		sum += p
	}
	return sum
}

func (g *byGen) ByRows() (int64, int64) { return g.fallback, g.total }

// Next formats the by values as the source group query casts them to
// VARCHAR and delegates to that group, or to the global generator.
func (g *byGen) Next(row RowCtx) (any, error) {
	vals := make([]sql.NullString, len(g.idx))
	for i, idx := range g.idx {
		if v := row.Values[idx]; v != nil {
			vals[i] = sql.NullString{String: formatValue(v, g.types[i]), Valid: true}
		}
	}
	g.total++
	gen, ok := g.groups[profile.GroupKey(vals)]
	if !ok {
		g.fallback++
		gen = g.global
	}
	return gen.Next(row)
}
