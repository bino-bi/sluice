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
	ErrNoWords     = errors.New("no word occurs minWordCount times in minGroupSize rows")
)

// sentenceRe matches one sentence: a run without ., ! and ? that holds
// at least one other character than a space.
const sentenceRe = `[^.!?]*[^.!?\s][^.!?]*`

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
// when the column reads number parts and the jitter is on. noise nil is
// off; with noise c holds the noisy counts (NoisyCounts) and every part
// takes its noisy variant (plan 4.3).
func (r *Runner) Column(ctx context.Context, t Table, col ColumnInfo, c Counts, needs Needs, s Settings, jitter *rng.Stream, noise *Noise) (*Column, error) {
	k := int64(s.MinGroupSize)
	out := &Column{Counts: c, NullRate: nullRate(c, k)}
	if needs.Parts == 0 {
		return out, nil
	}
	p := &colRun{ctx: ctx, r: r, from: t.sql(), x: parser.QuoteIdent(col.Name), name: col.Name, typ: col.Type, kind: KindOf(col.Type), k: k, noise: noise}
	if c.NonNull < k {
		return nil, p.fail(ErrTooFewRows)
	}
	if err := p.check(needs); err != nil {
		return nil, err
	}
	if _, ok := Measurements(needs); noise != nil && !ok {
		return nil, p.fail(ErrNoiseUnsupported)
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
		{numberParts, func() error {
			if noise != nil {
				return p.noisyNumbers(needs, s, jitter, out)
			}
			return p.numbers(needs, s, jitter, out)
		}},
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
			if noise != nil {
				return p.noisyFixed(&out.Weekdays, "weekdays", "dayofweek("+p.x+")", p.x+" IS NOT NULL", 0, 6)
			}
			return p.nominal(&out.Weekdays, &out.Suppressed, "weekday", "dayofweek("+p.x+")", p.x+" IS NOT NULL", 0)
		}},
		{PartMonths, func() error {
			if noise != nil {
				return p.noisyFixed(&out.Months, "months", "month("+p.x+")", p.x+" IS NOT NULL", 1, 12)
			}
			return p.nominal(&out.Months, &out.Suppressed, "month", "month("+p.x+")", p.x+" IS NOT NULL", 0)
		}},
		{PartAges, func() error { return p.ages(needs, s, out) }},
		{PartTimes, func() error {
			expr, where := p.timeSlot()
			if noise != nil {
				return p.noisyFixed(&out.Times, "times", expr, where, 0, slots-1)
			}
			return p.nominal(&out.Times, &out.Suppressed, "time", expr, where, 0)
		}},
		{PartLengths, func() error {
			if noise != nil {
				return p.noisyOrdinal(&out.Lengths, "lengths", "length("+p.x+")", p.x+" IS NOT NULL", 0)
			}
			return p.ordinal(&out.Lengths, &out.MergedBins, "length("+p.x+")", p.x+" IS NOT NULL")
		}},
		{PartWordCounts, func() error {
			expr := "len(regexp_split_to_array(trim(" + p.x + "), '\\s+'))"
			if noise != nil {
				return p.noisyOrdinal(&out.WordCounts, "words", expr, "trim("+p.x+") <> ''", 1)
			}
			return p.ordinal(&out.WordCounts, &out.MergedBins, expr, "trim("+p.x+") <> ''")
		}},
		{PartCharClasses, func() error { return p.charClasses(&out.CharClasses) }},
		{PartFanOut, func() error { return p.fanOut(t, *needs.Parent, out) }},
		{PartVocabulary, func() error { return p.vocabulary(needs, out) }},
		{PartSentences, func() error {
			return p.ordinal(&out.Sentences, &out.MergedBins, "len(regexp_extract_all("+p.x+", '"+sentenceRe+"'))", "trim("+p.x+") <> ''")
		}},
		{PartSentenceWords, func() error { return p.sentenceWords(&out.SentenceWords, &out.MergedBins) }},
		{PartTerminators, func() error {
			return p.listNominal(&out.Terminators, &out.Suppressed, "regexp_extract_all(sluice_x, '"+sentenceRe+"([.!?])', 1)", "true", 0, 0)
		}},
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
	// noise is nil without noise.
	noise *Noise
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
		parts&(textParts|wordParts) != 0 && p.kind != KindText ||
		parts&dateParts != 0 && p.kind != KindDate && p.kind != KindTimestamp ||
		parts&PartTimes != 0 && p.kind != KindTime && p.kind != KindTimestamp ||
		parts&PartTopValues != 0 && p.kind == KindOther ||
		parts&PartFanOut != 0 && (needs.Parent == nil || p.kind != KindInteger && p.kind != KindText && p.kind != KindUUID)
	if bad {
		return p.fail(ErrKind)
	}
	return nil
}

// numbers reads the number parts and applies clip (step 3), grid
// (step 4), bin merge (step 6), jitter (step 7) and spike check (step 8).
func (p *colRun) numbers(needs Needs, s Settings, jitter *rng.Stream, out *Column) error {
	e := p.axis
	lo, hi, err := p.sourceRange(s)
	if err != nil {
		return err
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

	if err := p.spikes(lo, hi, s, out); err != nil {
		return err
	}

	// The type step is a floor: a smaller move rounds back to the
	// source bound in the stage or the final CAST.
	unit := max(needs.Unit, defaultUnit(p.typ))
	out.Lo, out.Hi = p.jitter(lo, hi, unit, needs, s, jitter)
	out.axisQ = append([]float64(nil), quantiles...)
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

// sourceRange reads the range of the number parts: the clip quantiles,
// or min and max.
func (p *colRun) sourceRange(s Settings) (float64, float64, error) {
	e := p.axis
	var lo, hi float64
	if s.Clip != nil {
		var qs []float64
		q := "SELECT quantile_cont(" + e + ", " + floatList([]float64{s.Clip.Lower, s.Clip.Upper}) +
			") FROM " + p.from + " WHERE isfinite(" + e + ")"
		if err := p.query(q, nil, func(rows *sql.Rows) error { return scanFloatList(rows, &qs) }); err != nil {
			return 0, 0, err
		}
		if len(qs) != 2 {
			return 0, 0, p.fail(ErrQuery)
		}
		lo, hi = qs[0], qs[1]
	} else {
		q := "SELECT min(" + e + "), max(" + e + ") FROM " + p.from + " WHERE isfinite(" + e + ")"
		if err := p.query(q, nil, func(rows *sql.Rows) error { return scanFloats(rows, &lo, &hi) }); err != nil {
			return 0, 0, err
		}
	}
	if !finite(lo, hi) {
		return 0, 0, p.fail(ErrTooFewRows)
	}
	return lo, hi, nil
}

// spikes applies 4.2 step 8 on [lo, hi].
func (p *colRun) spikes(lo, hi float64, s Settings, out *Column) error {
	if !s.SpikeWarning || hi <= lo {
		return nil
	}
	e := p.axis
	r := hi - lo
	var low, high int64
	q := "SELECT count(*) FILTER (WHERE " + e + " <= ?), count(*) FILTER (WHERE " + e + " >= ?) FROM " + p.from +
		" WHERE " + e + " BETWEEN ? AND ?"
	args := []any{lo + float64(0.01*r), hi - float64(0.01*r), lo, hi}
	if err := p.query(q, args, func(rows *sql.Rows) error { return rows.Scan(&low, &high) }); err != nil {
		return err
	}
	out.Spikes = spikesOf(low, high, p.k)
	return nil
}

// jitter applies 4.2 step 7 to [lo, hi] and keeps the sign with
// KeepSign.
func (p *colRun) jitter(lo, hi, unit float64, needs Needs, s Settings, r *rng.Stream) (float64, float64) {
	jlo, jhi := jitterBounds(lo, hi, unit, s, r)
	if needs.KeepSign && lo >= 0 {
		jlo = max(jlo, 0)
		jhi = max(jhi, jlo)
	}
	return jlo, jhi
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
	rest := total - kept
	if p.noise != nil {
		values, rest = p.noisyNominal(what+"s", values, rest)
	}
	if len(values) == 0 {
		return p.fail(fmt.Errorf("%w (%s)", ErrNoGroups, what))
	}
	*dst = Nominal{Values: values, Rest: rest}
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

// fanOut reads the fan-out histogram and the orphan rate of a key column
// against its parent (plan 1.8 steps 3 and 4). Bins with fewer than k
// parents are merged, so no single parent's fan-out shows.
func (p *colRun) fanOut(t Table, ref Ref, out *Column) error {
	parent := Table{Catalog: t.Catalog, Schema: ref.Schema, Name: ref.Table}.sql()
	pk := parser.QuoteIdent(ref.Column)
	keys := "SELECT DISTINCT " + pk + " AS p FROM " + parent + " WHERE " + pk + " IS NOT NULL"
	q := "WITH k AS (" + keys + "), " +
		"c AS (SELECT " + p.x + " AS fk, count(*) AS n FROM " + p.from + " WHERE " + p.x + " IS NOT NULL GROUP BY 1) " +
		"SELECT coalesce(c.n, 0) AS f, count(*) FROM k LEFT JOIN c ON c.fk = k.p GROUP BY f ORDER BY f"
	var bins []Bin
	var parents int64
	err := p.query(q, nil, func(rows *sql.Rows) error {
		var f, c int64
		if err := rows.Scan(&f, &c); err != nil {
			return err
		}
		bins = append(bins, Bin{Lo: float64(f), Hi: float64(f), Count: c})
		parents += c
		return nil
	})
	if err != nil {
		return err
	}
	if parents < p.k {
		return p.fail(ErrTooFewRows)
	}
	var n int
	out.FanOut, n = mergeOrdinal(bins, p.k)
	out.MergedBins += n
	var orphans, total int64
	q = "SELECT count(*) FILTER (WHERE k.p IS NULL), count(*) FROM (SELECT " + p.x + " AS fk FROM " + p.from +
		" WHERE " + p.x + " IS NOT NULL) AS c LEFT JOIN (" + keys + ") AS k ON k.p = c.fk"
	if err := p.query(q, nil, func(rows *sql.Rows) error { return rows.Scan(&orphans, &total) }); err != nil {
		return err
	}
	if orphans >= p.k {
		out.OrphanRate = float64(orphans) / float64(total)
	}
	return nil
}

// textRows is a CTE that numbers the non-null rows, with the column as
// sluice_x. The numbering is not stable between runs, but count(DISTINCT
// rn) per group is.
func (p *colRun) textRows() string {
	return "r AS (SELECT row_number() OVER () AS rn, " + p.x + " AS sluice_x FROM " + p.from + " WHERE " + p.x + " IS NOT NULL)"
}

// listNominal reads a nominal part whose values come from the list
// expression list over sluice_x, many per row. A value is kept when it
// occurs minCount times in at least k rows; where filters the values v.
// Only the number of dropped values leaves DuckDB. limit 0 means no
// limit. No kept value gives an empty dst.
func (p *colRun) listNominal(dst *Nominal, suppressed *int64, list, where string, minCount int64, limit int) error {
	if limit <= 0 {
		limit = math.MaxInt32
	}
	q := "WITH " + p.textRows() + ", " +
		"e AS (SELECT rn, unnest(" + list + ") AS v FROM r), " +
		"g AS (SELECT v, count(*) AS c, count(DISTINCT rn) AS n FROM e WHERE " + where + " GROUP BY v), " +
		"kept AS (SELECT v, c FROM g WHERE c >= ? AND n >= ? ORDER BY c DESC, v LIMIT ?) " +
		"SELECT false AS summary, v, c, 0 AS dropped FROM kept " +
		"UNION ALL SELECT true, NULL, coalesce(sum(c), 0), count(*) FILTER (WHERE c < ? OR n < ?) FROM g " +
		"ORDER BY summary, c DESC, v"
	var total, dropped, kept int64
	var values []Value
	err := p.query(q, []any{minCount, p.k, int64(limit), minCount, p.k}, func(rows *sql.Rows) error {
		var summary bool
		var v sql.NullString
		var c, d int64
		if err := rows.Scan(&summary, &v, &c, &d); err != nil {
			return err
		}
		if summary {
			total, dropped = c, d
			return nil
		}
		values = append(values, Value{V: v.String, Count: c})
		kept += c
		return nil
	})
	if err != nil {
		return err
	}
	*dst = Nominal{Values: values, Rest: total - kept}
	*suppressed += dropped
	return nil
}

// vocabulary reads the words of a text column (plan 1.11). Words are
// letter runs only, so ids and amounts in free text never become words.
func (p *colRun) vocabulary(needs Needs, out *Column) error {
	err := p.listNominal(&out.Vocabulary, &out.Suppressed, "regexp_extract_all(lower(sluice_x), '\\p{L}+')",
		"length(v) BETWEEN 2 AND 40", int64(needs.MinWordCount), needs.MaxWords)
	if err != nil {
		return err
	}
	if len(out.Vocabulary.Values) == 0 {
		return p.fail(ErrNoWords)
	}
	return nil
}

// sentenceWords reads the words per sentence histogram. A bin counts
// sentences, and one row can have sentences in many bins, so the merge
// works on distinct rows: after each pass the rows of every merged bin
// are counted again in DuckDB, and a bin still below k merges again.
func (p *colRun) sentenceWords(dst *[]Bin, merged *int) error {
	w := "WITH " + p.textRows() + ", " +
		"s AS (SELECT rn, unnest(regexp_extract_all(sluice_x, '" + sentenceRe + "')) AS s FROM r), " +
		"w AS (SELECT rn, len(regexp_extract_all(s, '\\S+')) AS m FROM s) "
	var bins []Bin
	var ranges [][2]int64
	err := p.query(w+"SELECT m, count(*), count(DISTINCT rn) FROM w GROUP BY m ORDER BY m", nil, func(rows *sql.Rows) error {
		var m, c, n int64
		if err := rows.Scan(&m, &c, &n); err != nil {
			return err
		}
		bins = append(bins, Bin{Lo: float64(m), Hi: float64(m), Count: c, Rows: n})
		ranges = append(ranges, [2]int64{m, m})
		return nil
	})
	if err != nil {
		return err
	}
	for len(bins) > 0 {
		out, spans, n := mergeSpans(bins, p.k)
		*merged += n
		next := make([][2]int64, len(out))
		short := false
		for j, sp := range spans {
			next[j] = [2]int64{ranges[sp[0]][0], ranges[sp[1]][1]}
			if sp[0] == sp[1] {
				continue
			}
			q := w + "SELECT count(DISTINCT rn) FROM w WHERE m BETWEEN ? AND ?"
			if err := p.query(q, []any{next[j][0], next[j][1]}, func(rows *sql.Rows) error { return rows.Scan(&out[j].Rows) }); err != nil {
				return err
			}
			short = short || out[j].Rows < p.k
		}
		bins, ranges = out, next
		if !short || len(bins) == 1 {
			break
		}
	}
	if len(bins) == 0 || len(bins) == 1 && bins[0].Rows < p.k {
		return p.fail(ErrTooFewRows)
	}
	*dst = bins
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
	k := p.k
	if p.noise != nil {
		c := p.noise.fixedCells("classes", []int64{u, l, d, sp}, classDelta, p.k)
		u, l, d, sp, k = c[0], c[1], c[2], c[3], 1
	}
	*dst = CharClasses{Upper: u >= k, Lower: l >= k, Digit: d >= k, Space: sp >= k}
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
		if c >= p.k || p.noise != nil {
			styles[s] = c
		}
		return nil
	})
	if err != nil {
		return err
	}
	if p.noise != nil {
		copy(styles[:], p.noise.fixedCells("cases", styles[:], 1, p.k))
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
