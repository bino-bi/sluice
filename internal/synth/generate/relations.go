// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
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

// expression is computed by DuckDB from the other columns of the row when
// the builder writes the snapshot. The generator only stages a marker: true,
// or NULL when the nullRate draw says NULL. The builder checks args.sql.
type expression struct{}

func (expression) Check(col Column, _ Args) error {
	return fits(col, apitypes.GenExpression, col.Kind() != profile.KindOther)
}

func (expression) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (expression) New(Column, *profile.Column, Args, Streams) (ColumnGen, error) {
	return exprMarker{}, nil
}

type exprMarker struct{}

func (exprMarker) Next(RowCtx) (any, error) { return true, nil }
func (exprMarker) Pool() int64              { return -1 }

// FanOutStats are the counts of a foreignKey column for the report.
type FanOutStats struct {
	// Parents is the number of parent keys.
	Parents int64
	// Scale is the child rows over the sum of the drawn fan-outs.
	Scale float64
	// Orphans is the number of drawn keys with no parent.
	Orphans int64
}

// FanOuter is a ColumnGen of a foreignKey column. FanOutStats is nil for
// any other column.
type FanOuter interface {
	FanOutStats() *FanOutStats
}

var errNoFanOut = errors.New("profile has no fan-out histogram")

type foreignKey struct{}

func (foreignKey) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenForeignKey, k == profile.KindInteger || k == profile.KindText || k == profile.KindUUID)
}

func (foreignKey) Needs(_ Column, args Args) profile.Needs {
	if !args.isSource("fanOut", true) && !args.isSource("orphanRate", false) {
		return profile.Needs{}
	}
	schema, table, _ := strings.Cut(args.strOr("table", ""), ".")
	return profile.Needs{Parts: profile.PartFanOut, Parent: &profile.Ref{Schema: schema, Table: table, Column: args.strOr("column", "")}}
}

// New draws a fan-out per parent key, scales the fan-outs to the row
// count and shuffles the slots (plan 1.8 step 5). Row i gets the key of
// slot i, or an orphan key.
func (foreignKey) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if col.Keys == nil || len(col.Keys.Values) == 0 {
		return nil, errors.New("foreignKey parent has no keys")
	}
	keys := col.Keys.Values
	f := make([]int64, len(keys))
	if args.isSource("fanOut", true) {
		if len(p.FanOut) == 0 {
			return nil, errNoFanOut
		}
		weights := make([]float64, len(p.FanOut))
		for i, b := range p.FanOut {
			weights[i] = float64(b.Count)
		}
		cum := cumulative(weights)
		for i := range f {
			f[i] = int64(p.FanOut[pickCum(cum, s.FanOut)].Lo)
		}
	} else {
		sp := args.span("fanOut")
		for i := range f {
			f[i] = int64(sp.min + s.FanOut.IntN(sp.max-sp.min+1))
		}
	}
	slots, scale := fanOutSlots(f, col.Rows)
	for i := len(slots) - 1; i > 0; i-- {
		j := s.Shuffle.IntN(i + 1)
		slots[i], slots[j] = slots[j], slots[i]
	}
	g := &fkGen{keys: keys, slots: slots, stats: FanOutStats{Parents: int64(len(keys)), Scale: scale}, orphans: s.Orphans, values: s.Values}
	if v, ok := args["orphanRate"].(float64); ok {
		g.rate = v
	} else if args.isSource("orphanRate", false) {
		g.rate = p.OrphanRate
	}
	if g.rate == 0 {
		return g, nil
	}
	switch col.Keys.Stage {
	case StageBigint:
		for _, k := range keys {
			g.next = max(g.next, k.(int64))
		}
	case StageVarchar:
		g.parents = make(map[string]struct{}, len(keys))
		for _, k := range keys {
			g.parents[k.(string)] = struct{}{}
		}
	default:
		return nil, errors.New("foreignKey orphanRate needs an integer, text or UUID key")
	}
	return g, nil
}

// fanOutSlots scales the fan-outs f to n slots with the largest
// remainder method and returns parent i g_i times, in parent order, and
// the scale n / sum f. Ties go to the lower index. A zero sum counts as
// a fan-out of 1 for every parent.
func fanOutSlots(f []int64, n int64) ([]int, float64) {
	var total uint64
	for _, x := range f {
		total += uint64(x)
	}
	if total == 0 {
		for i := range f {
			f[i] = 1
		}
		total = uint64(len(f))
	}
	g := make([]int64, len(f))
	rem := make([]uint64, len(f))
	var sum int64
	for i, x := range f {
		// 128-bit product: f_i <= total, so the quotient fits and Div64
		// does not panic.
		hi, lo := bits.Mul64(uint64(x), uint64(n))
		q, r := bits.Div64(hi, lo, total)
		g[i], rem[i] = int64(q), r
		sum += int64(q)
	}
	idx := make([]int, len(f))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rem[idx[a]] > rem[idx[b]] })
	for _, i := range idx[:n-sum] {
		g[i]++
	}
	slots := make([]int, 0, n)
	for i, x := range g {
		for range x {
			slots = append(slots, i)
		}
	}
	return slots, float64(n) / float64(total)
}

// fkGen hands out the parent keys of the slots and draws orphans.
type fkGen struct {
	keys    []any
	slots   []int
	stats   FanOutStats
	rate    float64
	orphans *rng.Stream
	values  *rng.Stream
	// next is the last integer orphan key, parents the text keys.
	next    int64
	parents map[string]struct{}
}

func (g *fkGen) Pool() int64 { return int64(len(g.keys)) }

func (g *fkGen) FanOutStats() *FanOutStats {
	s := g.stats
	return &s
}

func (g *fkGen) Next(row RowCtx) (any, error) {
	if g.rate > 0 && g.orphans.Float64() < g.rate {
		g.stats.Orphans++
		if g.parents == nil {
			if g.next == math.MaxInt64 {
				return nil, errors.New("foreignKey has no integer left for an orphan key")
			}
			g.next++
			return g.next, nil
		}
		for {
			k := uuidV4(g.values)
			if _, hit := g.parents[k]; !hit {
				return k, nil
			}
		}
	}
	if row.Index < 0 || row.Index >= int64(len(g.slots)) {
		return nil, fmt.Errorf("foreignKey has no slot for row %d", row.Index)
	}
	return g.keys[g.slots[row.Index]], nil
}
