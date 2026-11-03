// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Order returns the evaluation order of a table's columns: every column
// after the columns it reads, the ordinal position as tie-break. refs[i]
// are the lower-case names column i reads (apitypes.SyntheticColumn.Refs),
// nil for none. With no refs the order is the ordinal order.
func Order(cols []Column, refs [][]string) ([]int, error) {
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		idx[strings.ToLower(c.Name)] = i
	}
	// readers[d] are the columns that read d; waiting[c] counts the
	// columns c reads that are not placed yet.
	readers := make([][]int, len(cols))
	waiting := make([]int, len(cols))
	for i, rs := range refs {
		for _, r := range rs {
			d, ok := idx[r]
			if !ok {
				return nil, fmt.Errorf("column %s: names column %s, which is not in the table", cols[i].Name, r)
			}
			readers[d] = append(readers[d], i)
			waiting[i]++
		}
	}
	order := make([]int, 0, len(cols))
	placed := make([]bool, len(cols))
	// Quadratic, but a table has few columns and the ordinal tie-break
	// needs no heap this way.
	for len(order) < len(cols) {
		next := -1
		for i := range cols {
			if !placed[i] && waiting[i] == 0 {
				next = i
				break
			}
		}
		if next < 0 {
			var left []string
			for i, c := range cols {
				if !placed[i] {
					left = append(left, c.Name)
				}
			}
			sort.Strings(left)
			return nil, fmt.Errorf("columns %s depend on each other", strings.Join(left, ", "))
		}
		placed[next] = true
		order = append(order, next)
		for _, r := range readers[next] {
			waiting[r]--
		}
	}
	return order, nil
}

type template struct{}

func (template) Check(col Column, args Args) error {
	if err := text(col, apitypes.GenTemplate); err != nil {
		return err
	}
	_, refs, err := apitypes.ParseTemplate(args.strOr("template", ""))
	if err != nil {
		return fmt.Errorf("template %w", err)
	}
	for _, r := range refs {
		if _, ok := col.Siblings[strings.ToLower(r)]; !ok {
			return fmt.Errorf("template names column %s, which is not in the table", r)
		}
	}
	return nil
}

func (template) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (template) New(col Column, _ *profile.Column, args Args, _ Streams) (ColumnGen, error) {
	lits, refs, err := apitypes.ParseTemplate(args.strOr("template", ""))
	if err != nil {
		return nil, fmt.Errorf("template %w", err)
	}
	g := &templateGen{lits: lits, lower: args.boolOr("lower", false)}
	for _, r := range refs {
		s, ok := col.Siblings[strings.ToLower(r)]
		if !ok {
			return nil, fmt.Errorf("template names column %s, which is not in the table", r)
		}
		g.refs = append(g.refs, s.Index)
		g.types = append(g.types, s.Type)
	}
	return g, nil
}

type templateGen struct {
	lits  []string
	refs  []int
	types []string
	lower bool
}

func (g *templateGen) Pool() int64 { return -1 }

// Next joins the literals and the formatted refs. A NULL ref gives NULL.
func (g *templateGen) Next(row RowCtx) (any, error) {
	var b strings.Builder
	b.WriteString(g.lits[0])
	for i, idx := range g.refs {
		v := row.Values[idx]
		if v == nil {
			return nil, nil
		}
		b.WriteString(formatValue(v, g.types[i]))
		b.WriteString(g.lits[i+1])
	}
	if g.lower {
		return strings.ToLower(b.String()), nil
	}
	return b.String(), nil
}

// formatValue renders a staged value as the snapshot column of type typ
// holds it. The write casts a float64 stage to DECIMAL, FLOAT, HUGEINT,
// UBIGINT or TIME, so the text rounds the same way DuckDB does there.
func formatValue(v any, typ string) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return formatFloat(x, typ)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		if profile.KindOf(typ) == profile.KindDate {
			// DuckDB has no year 0: Go year -5 is 0006 (BC).
			if y := x.UTC().Year(); y <= 0 {
				return fmt.Sprintf("%04d-%s (BC)", 1-y, x.UTC().Format("01-02"))
			}
			return x.UTC().Format("2006-01-02")
		}
		return x.UTC().Format("2006-01-02 15:04:05.999999")
	}
	return fmt.Sprint(v)
}

func formatFloat(x float64, typ string) string {
	t := strings.ToUpper(strings.TrimSpace(typ))
	var prec, scale int
	switch kind := profile.KindOf(t); {
	case kind == profile.KindTime:
		// to_seconds rounds half to even on microseconds; TIME wraps at midnight.
		const day = 86400 * 1e6
		us := math.Mod(math.RoundToEven(x*1e6), day)
		if us < 0 {
			us += day
		}
		return time.UnixMicro(int64(us)).UTC().Format("15:04:05.999999")
	case kind == profile.KindInteger:
		// Only the types past int64 stage as float64; DuckDB rounds half to even.
		return strconv.FormatFloat(math.RoundToEven(x), 'f', 0, 64)
	case t == "FLOAT":
		return strconv.FormatFloat(float64(float32(x)), 'f', -1, 32)
	case strings.HasPrefix(t, "DECIMAL("):
		if n, _ := fmt.Sscanf(t, "DECIMAL(%d,%d)", &prec, &scale); n == 2 {
			return formatDecimal(x, scale)
		}
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// formatDecimal renders x as DECIMAL(p,scale) text. The DOUBLE to DECIMAL
// cast rounds x*10^scale half away from zero.
func formatDecimal(x float64, scale int) string {
	n := math.Round(x * math.Pow10(scale))
	neg := n < 0
	d := strconv.FormatFloat(math.Abs(n), 'f', 0, 64)
	if scale > 0 {
		if len(d) <= scale {
			d = strings.Repeat("0", scale-len(d)+1) + d
		}
		d = d[:len(d)-scale] + "." + d[len(d)-scale:]
	}
	if neg {
		return "-" + d
	}
	return d
}

type dateOffset struct{}

// fromSibling returns the from column of a dateOffset.
func fromSibling(col Column, args Args) (Sibling, error) {
	from := args.strOr("from", "")
	s, ok := col.Siblings[strings.ToLower(from)]
	if !ok {
		return Sibling{}, fmt.Errorf("dateOffset names column %s, which is not in the table", from)
	}
	return s, nil
}

func (dateOffset) Check(col Column, args Args) error {
	k := col.Kind()
	if err := fits(col, apitypes.GenDateOffset, isDate(k)); err != nil {
		return err
	}
	s, err := fromSibling(col, args)
	if err != nil {
		return err
	}
	if profile.KindOf(s.Type) != k {
		want := "DATE"
		if k == profile.KindTimestamp {
			want = "TIMESTAMP"
		}
		return fmt.Errorf("dateOffset needs a %s column in from, %s has type %s", want, s.Name, profile.DisplayType(s.Type))
	}
	if span, ok := args["offset"].(map[string]any); ok && k == profile.KindDate {
		lo, _ := span["min"].(float64)
		hi, _ := span["max"].(float64)
		if math.Ceil(lo) > math.Floor(hi) {
			return fmt.Errorf("dateOffset on a DATE needs a whole day in the offset span, {min: %v, max: %v} has none", lo, hi)
		}
	}
	return nil
}

func (dateOffset) Needs(col Column, args Args) profile.Needs {
	s, _ := fromSibling(col, args)
	// The draw rounds to whole days or seconds, so a smaller jitter move
	// would round back to the source bound.
	n := profile.Needs{Parts: profile.PartOffset, From: s.Name, KeepSign: true, Unit: 1}
	if args.isSource("offset", true) {
		n.Parts |= profile.PartQuantiles
	}
	return n
}

func (dateOffset) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	from, err := fromSibling(col, args)
	if err != nil {
		return nil, err
	}
	g := &offsetGen{from: from.Index, date: col.Kind() == profile.KindDate, r: s.Values}
	if args.isSource("offset", true) {
		if len(p.Quantiles) < 2 {
			return nil, errNoGrid
		}
		g.lo, g.hi = p.Quantiles[0], p.Quantiles[len(p.Quantiles)-1]
		g.draw = func() float64 { return gridValue(p.Quantiles, g.r.Float64(), true) }
		return g, nil
	}
	span, _ := args["offset"].(map[string]any)
	lo, _ := span["min"].(float64)
	hi, _ := span["max"].(float64)
	if !g.date {
		lo, hi = lo*secondsPerDay, hi*secondsPerDay
	}
	g.lo, g.hi = lo, hi
	g.draw = func() float64 { return lo + float64(g.r.Float64()*(hi-lo)) }
	return g, nil
}

// offsetGen adds a drawn offset to the from value of the row: whole days
// on a DATE, whole seconds on a TIMESTAMP.
type offsetGen struct {
	from   int
	date   bool
	lo, hi float64
	r      *rng.Stream
	draw   func() float64
}

func (g *offsetGen) Pool() int64 { return -1 }

func (g *offsetGen) Next(row RowCtx) (any, error) {
	// The draw happens on every row, so a NULL from does not shift the
	// values of later rows.
	o := rounding{decimals: 0}.fit(g.draw(), g.lo, g.hi)
	t, ok := row.Values[g.from].(time.Time)
	if !ok {
		return nil, nil
	}
	if g.date {
		return t.AddDate(0, 0, int(o)), nil
	}
	// Seconds, not a time.Duration: a source offset to a sentinel like
	// 9999-12-31 is above the 292 years a Duration holds.
	return time.Unix(t.Unix()+int64(o), int64(t.Nanosecond())).In(t.Location()), nil
}
