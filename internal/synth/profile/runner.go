// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/rng"
)

// Errors of a column profile. They are wrapped with the column name and
// never carry a value of the source.
var (
	ErrTooFewRows  = errors.New("fewer than minGroupSize non-null rows")
	ErrGrid        = errors.New("too few rows for a quantile grid, use uniform or range")
	ErrNotPositive = errors.New("lognormal needs values above 0")
	ErrNoGroups    = errors.New("no group has minGroupSize rows, use a generator without a profile")
	ErrKind        = errors.New("profile part does not fit the column type")
	ErrQuery       = errors.New("profile query failed")
)

// Runner runs profile queries on one connection of the builder instance.
// The connection must have TimeZone set to UTC, so weekdays and months
// of TIMESTAMP WITH TIME ZONE do not depend on the host.
type Runner struct {
	Conn *sql.Conn
	// Log gets the raw DuckDB error text at debug level. Nil uses
	// slog.Default.
	Log *slog.Logger
}

func (t Table) sql() string {
	return parser.QuoteIdent(t.Catalog) + "." + parser.QuoteIdent(t.Schema) + "." + parser.QuoteIdent(t.Name)
}

// Base runs the one base query of a table: row count, and per column the
// non-null and distinct counts. Columns of KindOther get no distinct
// count, because DuckDB cannot compare every nested type.
func (r *Runner) Base(ctx context.Context, t Table, cols []ColumnInfo) ([]Counts, error) {
	sel := []string{"count(*)"}
	for _, c := range cols {
		x := parser.QuoteIdent(c.Name)
		d := "count(DISTINCT " + x + ")"
		if KindOf(c.Type) == KindOther {
			d = "0"
		}
		sel = append(sel, "count("+x+")", d)
	}
	dest := make([]any, 1+2*len(cols))
	var rows int64
	dest[0] = &rows
	out := make([]Counts, len(cols))
	for i := range out {
		dest[1+2*i] = &out[i].NonNull
		dest[2+2*i] = &out[i].Distinct
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM " + t.sql()
	if err := r.Conn.QueryRowContext(ctx, q).Scan(dest...); err != nil {
		r.logger().Debug("synthetic base query failed", "table", t.Schema+"."+t.Name, "error", err)
		return nil, fmt.Errorf("table %s.%s: %w", t.Schema, t.Name, ErrQuery)
	}
	for i := range out {
		out[i].Rows = rows
	}
	return out, nil
}

// Column runs the profile parts of needs on one column and applies the
// privacy filter. jitter is the column's jitter stream. It is read only
// when the column reads number parts and the jitter is on.
func (r *Runner) Column(ctx context.Context, t Table, col ColumnInfo, c Counts, needs Needs, s Settings, jitter *rng.Stream) (*Column, error) {
	k := int64(s.MinGroupSize)
	out := &Column{Counts: c, NullRate: nullRate(c, k)}
	if needs.Parts == 0 {
		return out, nil
	}
	p := &colRun{ctx: ctx, r: r, from: t.sql(), x: parser.QuoteIdent(col.Name), name: col.Name, typ: col.Type, kind: KindOf(col.Type), k: k}
	if c.NonNull < k {
		return nil, p.fail(ErrTooFewRows)
	}
	if err := p.check(needs); err != nil {
		return nil, err
	}
	p.axis = p.kind.axis(p.x)
	if needs.Parts&PartOffset != 0 {
		f := parser.QuoteIdent(needs.From)
		p.axis = p.kind.offsetAxis(p.x, f)
		if err := p.offsetNullRate(f, out); err != nil {
			return nil, err
		}
	}
	steps := []struct {
		part Part
		run  func() error
	}{
		{numberParts, func() error { return p.numbers(needs, s, jitter, out) }},
		{PartTopValues, func() error {
			return p.nominal(&out.TopValues, &out.Suppressed, "value", p.x, p.x+" IS NOT NULL", needs.MaxValues)
		}},
		{PartInitials, func() error {
			return p.nominal(&out.Initials, &out.Suppressed, "initial", "left("+p.x+", 1)", p.x+" <> ''", 0)
		}},
		{PartPatterns, func() error {
			return p.nominal(&out.Patterns, &out.Suppressed, "pattern", patternExpr(p.x),
				p.x+" IS NOT NULL AND length("+p.x+") <= 64", needs.MaxPatterns)
		}},
		{PartPrefixes, func() error {
			n := strconv.Itoa(needs.PrefixLength)
			return p.nominal(&out.Prefixes, &out.Suppressed, "prefix", "left("+p.x+", "+n+")",
				p.x+" IS NOT NULL AND length("+p.x+") >= "+n, 0)
		}},
		{PartWeekdays, func() error {
			return p.nominal(&out.Weekdays, &out.Suppressed, "weekday", "dayofweek("+p.x+")", p.x+" IS NOT NULL", 0)
		}},
		{PartMonths, func() error {
			return p.nominal(&out.Months, &out.Suppressed, "month", "month("+p.x+")", p.x+" IS NOT NULL", 0)
		}},
		{PartLengths, func() error {
			return p.ordinal(&out.Lengths, &out.MergedBins, "length("+p.x+")", p.x+" IS NOT NULL")
		}},
		{PartWordCounts, func() error {
			return p.ordinal(&out.WordCounts, &out.MergedBins,
				"len(regexp_split_to_array(trim("+p.x+"), '\\s+'))", "trim("+p.x+") <> ''")
		}},
		{PartCharClasses, func() error { return p.charClasses(&out.CharClasses) }},
		{PartCaseStyles, func() error { return p.caseStyles(&out.CaseStyles) }},
	}
	for _, st := range steps {
		if needs.Parts&st.part == 0 {
			continue
		}
		if err := st.run(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// colRun holds the state of one column profile.
type colRun struct {
	ctx  context.Context
	r    *Runner
	from string // quoted table
	x    string // quoted column
	name string
	typ  string // data_type
	kind Kind
	k    int64
	// axis is the number axis of the number parts.
	axis string
}

func (p *colRun) fail(err error) error {
	return fmt.Errorf("column %s: %w", p.name, err)
}

// query runs q and keeps the raw DuckDB error, which may quote a value,
// out of the returned error.
func (p *colRun) query(q string, args []any, scan func(*sql.Rows) error) error {
	rows, err := p.r.Conn.QueryContext(p.ctx, q, args...)
	if err == nil {
		for rows.Next() {
			if err = scan(rows); err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
	}
	if err != nil {
		p.r.logger().Debug("synthetic profile query failed", "column", p.name, "error", err)
		return p.fail(ErrQuery)
	}
	return nil
}

func (p *colRun) check(needs Needs) error {
	parts := needs.Parts
	bad := parts&numberParts != 0 && !p.kind.Numeric() ||
		parts&PartOffset != 0 && (p.kind != KindDate && p.kind != KindTimestamp || needs.From == "") ||
		parts&textParts != 0 && p.kind != KindText ||
		parts&dateParts != 0 && p.kind != KindDate && p.kind != KindTimestamp ||
		parts&PartTopValues != 0 && p.kind == KindOther
	if bad {
		return p.fail(ErrKind)
	}
	return nil
}

// numbers reads the number parts and applies clip (step 3), grid
// (step 4), bin merge (step 6), jitter (step 7) and spike check (step 8).
func (p *colRun) numbers(needs Needs, s Settings, jitter *rng.Stream, out *Column) error {
	e := p.axis
	var lo, hi float64
	if s.Clip != nil {
		var qs []float64
		q := "SELECT quantile_cont(" + e + ", " + floatList([]float64{s.Clip.Lower, s.Clip.Upper}) +
			") FROM " + p.from + " WHERE isfinite(" + e + ")"
		if err := p.query(q, nil, func(rows *sql.Rows) error { return scanFloatList(rows, &qs) }); err != nil {
			return err
		}
		if len(qs) != 2 {
			return p.fail(ErrQuery)
		}
		lo, hi = qs[0], qs[1]
	} else {
		q := "SELECT min(" + e + "), max(" + e + ") FROM " + p.from + " WHERE isfinite(" + e + ")"
		if err := p.query(q, nil, func(rows *sql.Rows) error { return scanFloats(rows, &lo, &hi) }); err != nil {
			return err
		}
	}
	if !finite(lo, hi) {
		return p.fail(ErrTooFewRows)
	}
	inRange := " FROM " + p.from + " WHERE " + e + " BETWEEN ? AND ?"
	bounds := []any{lo, hi}

	// Sums of DOUBLE values depend on the thread schedule in the last
	// bits. Ordered aggregates add in value order, so the result is the
	// same for every thread count (probe in profile_test.go).
	var minIn float64
	sel := "SELECT count(*), min(" + e + ")"
	floats := []*float64{&minIn}
	if needs.Parts&PartStats != 0 {
		sel += ", avg(" + e + " ORDER BY " + e + "), stddev_samp(" + e + " ORDER BY " + e + ")"
		floats = append(floats, &out.Mean, &out.StdDev)
	}
	if err := p.query(sel+inRange, bounds, func(rows *sql.Rows) error { return scanCountFloats(rows, &out.InRange, floats...) }); err != nil {
		return err
	}
	if out.InRange < p.k || !finite(minIn, out.Mean, out.StdDev) {
		return p.fail(ErrTooFewRows)
	}

	var quantiles []float64
	if needs.Parts&PartQuantiles != 0 {
		steps := gridSteps(out.InRange, p.k)
		if steps < 2 {
			return p.fail(ErrGrid)
		}
		probs := make([]float64, steps+1)
		for i := range probs {
			probs[i] = float64(i) / float64(steps)
		}
		q := "SELECT quantile_cont(" + e + ", " + floatList(probs) + ")" + inRange
		if err := p.query(q, bounds, func(rows *sql.Rows) error { return scanFloatList(rows, &quantiles) }); err != nil {
			return err
		}
		if len(quantiles) != len(probs) {
			return p.fail(ErrQuery)
		}
	}

	if needs.Parts&PartLogStats != 0 {
		if minIn <= 0 {
			return p.fail(ErrNotPositive)
		}
		l := "ln(" + e + ")"
		q := "SELECT avg(" + l + " ORDER BY " + l + "), stddev_samp(" + l + " ORDER BY " + l + ")" + inRange
		if err := p.query(q, bounds, func(rows *sql.Rows) error { return scanFloats(rows, &out.LogMean, &out.LogStdDev) }); err != nil {
			return err
		}
	}

	var bins []Bin
	if needs.Parts&PartBins != 0 {
		var err error
		if bins, err = p.bins(e, inRange, bounds, lo, hi, max(needs.Bins, 1)); err != nil {
			return err
		}
		bins, out.MergedBins = mergeOrdinal(bins, p.k)
	}

	if s.SpikeWarning && hi > lo {
		r := hi - lo
		var low, high int64
		q := "SELECT count(*) FILTER (WHERE " + e + " <= ?), count(*) FILTER (WHERE " + e + " >= ?)" + inRange
		args := []any{lo + float64(0.01*r), hi - float64(0.01*r), lo, hi}
		if err := p.query(q, args, func(rows *sql.Rows) error { return rows.Scan(&low, &high) }); err != nil {
			return err
		}
		out.Spikes = spikesOf(low, high, p.k)
	}

	// The type step is a floor: a smaller move rounds back to the
	// source bound in the stage or the final CAST.
	unit := max(needs.Unit, defaultUnit(p.typ))
	out.Lo, out.Hi = jitterBounds(lo, hi, unit, s, jitter)
	if needs.KeepSign && lo >= 0 {
		out.Lo = max(out.Lo, 0)
		out.Hi = max(out.Hi, out.Lo)
	}
	for i, q := range quantiles {
		quantiles[i] = mapLinear(q, lo, hi, out.Lo, out.Hi)
	}
	out.Quantiles = quantiles
	for i := range bins {
		bins[i].Lo = mapLinear(bins[i].Lo, lo, hi, out.Lo, out.Hi)
		bins[i].Hi = mapLinear(bins[i].Hi, lo, hi, out.Lo, out.Hi)
	}
	out.Bins = bins
	return nil
}

// offsetNullRate sets the null rate of x among the rows where f is not
// NULL, with the k rule of nullRate. A NULL f gives a NULL value anyway,
// so this rate gives the source NULL share overall.
func (p *colRun) offsetNullRate(f string, out *Column) error {
	var c Counts
	q := "SELECT count(*), count(" + p.x + ") FROM " + p.from + " WHERE " + f + " IS NOT NULL"
	if err := p.query(q, nil, func(rows *sql.Rows) error { return rows.Scan(&c.Rows, &c.NonNull) }); err != nil {
		return err
	}
	out.NullRate = nullRate(c, p.k)
	return nil
}

// bins reads an equal-width histogram on [lo, hi]. Empty bins are kept
// with count 0, so the merge sees every neighbor.
func (p *colRun) bins(e, inRange string, bounds []any, lo, hi float64, n int) ([]Bin, error) {
	width := (hi - lo) / float64(n)
	bins := make([]Bin, n)
	for i := range bins {
		bins[i].Lo = lo + float64(float64(i)*width)
		bins[i].Hi = lo + float64(float64(i+1)*width)
	}
	bins[n-1].Hi = hi
	if width == 0 {
		var c int64
		if err := p.query("SELECT count(*)"+inRange, bounds, func(rows *sql.Rows) error { return rows.Scan(&c) }); err != nil {
			return nil, err
		}
		return []Bin{{Lo: lo, Hi: hi, Count: c}}, nil
	}
	q := "SELECT least(CAST(floor((" + e + " - ?) / ?) AS BIGINT), ?) AS b, count(*)" + inRange + " GROUP BY b ORDER BY b"
	args := append([]any{lo, width, int64(n - 1)}, bounds...)
	err := p.query(q, args, func(rows *sql.Rows) error {
		var b, c int64
		if err := rows.Scan(&b, &c); err != nil {
			return err
		}
		if b < 0 || b >= int64(n) {
			return fmt.Errorf("bin %d out of range", b)
		}
		bins[b].Count = c
		return nil
	})
	return bins, err
}

// nominal reads one nominal part. Groups with fewer than k rows are
// dropped inside DuckDB (4.2 step 5); only their number comes back.
// limit 0 means no limit.
func (p *colRun) nominal(dst *Nominal, suppressed *int64, what, expr, where string, limit int) error {
	if limit <= 0 {
		limit = math.MaxInt32
	}
	q := "WITH g AS (SELECT " + expr + " AS v, count(*) AS c FROM " + p.from + " WHERE " + where + " GROUP BY 1), " +
		"kept AS (SELECT v, c FROM g WHERE c >= ? ORDER BY c DESC, v LIMIT ?) " +
		"SELECT false AS summary, v, c, 0 AS dropped FROM kept " +
		"UNION ALL SELECT true, NULL, coalesce(sum(c), 0), count(*) FILTER (WHERE c < ?) FROM g " +
		"ORDER BY summary, c DESC, v"
	var total, dropped, kept int64
	var values []Value
	err := p.query(q, []any{p.k, int64(limit), p.k}, func(rows *sql.Rows) error {
		var summary bool
		var v any
		var c, d int64
		if err := rows.Scan(&summary, &v, &c, &d); err != nil {
			return err
		}
		if summary {
			total, dropped = c, d
			return nil
		}
		values = append(values, Value{V: v, Count: c})
		kept += c
		return nil
	})
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return p.fail(fmt.Errorf("%w (%s)", ErrNoGroups, what))
	}
	*dst = Nominal{Values: values, Rest: total - kept}
	*suppressed += dropped
	return nil
}

// ordinal reads one ordinal part with one bin per value of expr and
// merges bins below k (4.2 step 6).
func (p *colRun) ordinal(dst *[]Bin, merged *int, expr, where string) error {
	q := "SELECT " + expr + " AS l, count(*) FROM " + p.from + " WHERE " + where + " GROUP BY l ORDER BY l"
	var bins []Bin
	var total int64
	err := p.query(q, nil, func(rows *sql.Rows) error {
		var l, c int64
		if err := rows.Scan(&l, &c); err != nil {
			return err
		}
		bins = append(bins, Bin{Lo: float64(l), Hi: float64(l), Count: c})
		total += c
		return nil
	})
	if err != nil {
		return err
	}
	if total < p.k {
		return p.fail(ErrTooFewRows)
	}
	var n int
	*dst, n = mergeOrdinal(bins, p.k)
	*merged += n
	return nil
}

func (p *colRun) charClasses(dst *CharClasses) error {
	q := "SELECT count(*) FILTER (WHERE regexp_matches(" + p.x + ", '\\p{Lu}')), " +
		"count(*) FILTER (WHERE regexp_matches(" + p.x + ", '\\p{Ll}')), " +
		"count(*) FILTER (WHERE regexp_matches(" + p.x + ", '\\p{Nd}')), " +
		"count(*) FILTER (WHERE contains(" + p.x + ", ' ')) FROM " + p.from + " WHERE " + p.x + " IS NOT NULL"
	var u, l, d, sp int64
	if err := p.query(q, nil, func(rows *sql.Rows) error { return rows.Scan(&u, &l, &d, &sp) }); err != nil {
		return err
	}
	*dst = CharClasses{Upper: u >= p.k, Lower: l >= p.k, Digit: d >= p.k, Space: sp >= p.k}
	if *dst == (CharClasses{}) {
		return p.fail(fmt.Errorf("%w (character class)", ErrNoGroups))
	}
	return nil
}

func (p *colRun) caseStyles(dst *[4]int64) error {
	x := p.x
	q := "SELECT CASE WHEN " + x + " = upper(" + x + ") AND " + x + " <> lower(" + x + ") THEN 0 " +
		"WHEN " + x + " = lower(" + x + ") AND " + x + " <> upper(" + x + ") THEN 1 " +
		"WHEN upper(left(" + x + ", 1)) || lower(substr(" + x + ", 2)) = " + x + " THEN 2 ELSE 3 END AS s, count(*) " +
		"FROM " + p.from + " WHERE " + x + " IS NOT NULL GROUP BY s ORDER BY s"
	var styles [4]int64
	err := p.query(q, nil, func(rows *sql.Rows) error {
		var s, c int64
		if err := rows.Scan(&s, &c); err != nil {
			return err
		}
		if c >= p.k {
			styles[s] = c
		}
		return nil
	})
	if err != nil {
		return err
	}
	if styles == ([4]int64{}) {
		return p.fail(fmt.Errorf("%w (case style)", ErrNoGroups))
	}
	*dst = styles
	return nil
}

// patternExpr maps letters to A or a and digits to 9. Other letters go
// first, so the A of the upper-case step is not replaced again.
func patternExpr(x string) string {
	e := "regexp_replace(" + x + ", '[\\p{Lt}\\p{Lm}\\p{Lo}]', 'a', 'g')"
	e = "regexp_replace(" + e + ", '\\p{Lu}', 'A', 'g')"
	e = "regexp_replace(" + e + ", '\\p{Ll}', 'a', 'g')"
	return "regexp_replace(" + e + ", '\\p{Nd}', '9', 'g')"
}

// floatList renders constant probabilities as a DOUBLE list literal.
// quantile_cont needs them as constants, so they cannot be parameters.
// They come from the spec or from row counts, never from a value.
func floatList(vs []float64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	return "CAST([" + strings.Join(parts, ", ") + "] AS DOUBLE[])"
}

type rowScanner interface{ Scan(dest ...any) error }

func scanFloats(rows rowScanner, dst ...*float64) error {
	return scanCountFloats(rows, nil, dst...)
}

// scanCountFloats scans an optional count and nullable floats. A NULL
// float becomes NaN.
func scanCountFloats(rows rowScanner, count *int64, dst ...*float64) error {
	vals := make([]sql.NullFloat64, len(dst))
	var ptrs []any
	if count != nil {
		ptrs = append(ptrs, count)
	}
	for i := range vals {
		ptrs = append(ptrs, &vals[i])
	}
	if err := rows.Scan(ptrs...); err != nil {
		return err
	}
	for i, v := range vals {
		*dst[i] = math.NaN()
		if v.Valid {
			*dst[i] = v.Float64
		}
	}
	return nil
}

func scanFloatList(rows *sql.Rows, dst *[]float64) error {
	var v any
	if err := rows.Scan(&v); err != nil {
		return err
	}
	list, ok := v.([]any)
	if !ok {
		return fmt.Errorf("quantile list has type %T", v)
	}
	out := make([]float64, len(list))
	for i, e := range list {
		f, ok := e.(float64)
		if !ok {
			return fmt.Errorf("quantile has type %T", e)
		}
		out[i] = f
	}
	*dst = out
	return nil
}
