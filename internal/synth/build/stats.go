// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// columnReport collects the report entry of one column after the
// snapshot table is written.
func (b *builder) columnReport(ctx context.Context, conn *sql.Conn, pt profile.Table, t *apitypes.SyntheticTable,
	c *column, rows int64) (ColumnReport, error) {
	cr := ColumnReport{
		Name:      c.info.Name,
		Type:      profile.DisplayType(c.info.Type),
		Generator: string(c.choice.Generator),
		ChosenBy:  c.choice.ChosenBy,
		Tags:      c.tags,
		Warnings:  []string{},
	}
	for _, r := range c.refs {
		cr.DependsOn = append(cr.DependsOn, c.sibs[r].Name)
	}
	if c.expr != nil {
		cr.DependsOn = append(cr.DependsOn, c.expr.refs...)
	}
	sort.Strings(cr.DependsOn)
	settings := c.settings
	if c.prof == nil {
		settings = profile.Effective(t.Privacy, b.spec.Privacy)
	}
	k := int64(settings.MinGroupSize)
	src := &Stats{NullRate: nullRate(c.counts, k), Distinct: c.counts.Distinct}
	cr.Source = src
	cr.Synthetic = &Stats{}
	if rows == 0 {
		return cr, nil
	}
	warn := func(w string) {
		cr.Warnings = append(cr.Warnings, w)
		b.rep.Warnings = append(b.rep.Warnings, t.Name+"."+c.info.Name+": "+w)
	}
	for _, w := range c.warnings {
		warn(w)
	}
	if c.by != nil {
		cr.By = b.byReport(t, c, warn)
	}
	if f, ok := c.gen.(generate.FanOuter); ok && f.FanOutStats() != nil {
		st := f.FanOutStats()
		e, _, _ := b.parentTarget(c)
		cr.FanOut = &FanOutReport{Parent: e.table + "." + e.column, Parents: st.Parents, Scale: st.Scale, OrphanRows: st.Orphans}
		if st.Scale < 0.5 || st.Scale > 2 {
			warn(fmt.Sprintf("the fan-outs are scaled by %.2f to fit the row count, the parent and child sizes do not fit the source fan-out", st.Scale))
		}
	}
	p := c.prof
	cr.SuppressedValues, cr.MergedBins = p.Suppressed, p.MergedBins
	if f, ok := c.gen.(interface{ Fallbacks() int64 }); ok {
		cr.Fallbacks = f.Fallbacks()
	}
	for _, s := range p.Spikes {
		b.rep.Spikes = append(b.rep.Spikes, SpikeReport{Table: t.Name, Column: c.info.Name, Side: s.Side, Rows: s.Rows})
		b.log.Warn("synthetic build: spike at a range end, consider clipOutliers",
			slog.String("name", b.name), slog.String("table", t.Name), slog.String("column", c.info.Name), slog.String("side", s.Side))
	}

	from := parser.QuoteIdent(pt.Catalog) + "." + parser.QuoteIdent(pt.Schema) + "." + parser.QuoteIdent(pt.Name)
	x := parser.QuoteIdent(c.info.Name)
	if e := profile.Axis(c.info.Type, x); e != "" && c.counts.NonNull >= 2*k {
		lo, hi := float64(k)/float64(c.counts.NonNull), 1-float64(k)/float64(c.counts.NonNull)
		// A clip below k/n would report the value of fewer than k rows.
		if settings.Clip != nil {
			lo, hi = max(lo, settings.Clip.Lower), min(hi, settings.Clip.Upper)
		}
		qs, mean, err := b.quantilesMean(ctx, conn, e, from, []float64{lo, 0.5, hi})
		if err != nil {
			return cr, fmt.Errorf("column %s: %w", c.info.Name, err)
		}
		if len(qs) == 3 {
			src.Min, src.Median, src.Max, src.Mean = ptr(qs[0]), ptr(qs[1]), ptr(qs[2]), mean
		}
	}

	target := outCatalog + "." + parser.QuoteIdent(pt.Schema) + "." + parser.QuoteIdent(pt.Name)
	syn, err := b.syntheticStats(ctx, conn, target, x, c.snapType)
	if err != nil {
		return cr, fmt.Errorf("column %s: %w", c.info.Name, err)
	}
	cr.Synthetic = syn
	if cr.Distance, err = b.distance(ctx, conn, target, x, c); err != nil {
		return cr, fmt.Errorf("column %s: %w", c.info.Name, err)
	}
	return cr, nil
}

// byReport collects the by counts of a column and warns when most draws
// used the global profile or a group has a spike.
func (b *builder) byReport(t *apitypes.SyntheticTable, c *column, warn func(string)) *ByReport {
	br := &ByReport{Groups: c.by.Kept, DroppedGroups: c.by.Dropped, SpikeGroups: c.by.SpikeGroups}
	by, _ := generate.BySiblings(generate.Column{Siblings: c.sibs}, c.choice.Args)
	for _, s := range by {
		br.Columns = append(br.Columns, s.Name)
	}
	sort.Strings(br.Columns)
	if bc, ok := c.gen.(generate.ByCounter); ok {
		fallback, total := bc.ByRows()
		br.FallbackRows = fallback
		if fallback*2 > total {
			warn("most rows use the global profile, the by columns do not match the source groups")
		}
	}
	if br.SpikeGroups > 0 {
		warn(fmt.Sprintf("%d by-groups have a spike at a bound", br.SpikeGroups))
		b.log.Warn("synthetic build: by-groups with a spike at a range end, consider clipOutliers",
			slog.String("name", b.name), slog.String("table", t.Name), slog.String("column", c.info.Name), slog.Int("groups", br.SpikeGroups))
	}
	return br
}

// nullRate is the filtered null rate of plan 4.2 step 2.
func nullRate(c profile.Counts, k int64) float64 {
	nulls := c.Rows - c.NonNull
	switch {
	case c.Rows == 0, nulls < k:
		return 0
	case c.NonNull < k:
		return 1
	}
	return float64(nulls) / float64(c.Rows)
}

func ptr(f float64) *float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

func floatList(vs []float64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	return "CAST([" + strings.Join(parts, ", ") + "] AS DOUBLE[])"
}

// quantilesMean reads quantile_cont at probs and the ordered mean of the
// number axis e. The ordered mean has the same bits for every thread
// count.
func (b *builder) quantilesMean(ctx context.Context, conn *sql.Conn, e, from string, probs []float64) ([]float64, *float64, error) {
	q := "SELECT quantile_cont(" + e + ", " + floatList(probs) + "), avg(" + e + " ORDER BY " + e + ") FROM " + from +
		" WHERE isfinite(" + e + ")"
	var list any
	var mean sql.NullFloat64
	if err := conn.QueryRowContext(ctx, q).Scan(&list, &mean); err != nil {
		return nil, nil, b.duckErr("read the report statistics", err)
	}
	qs, err := floats(list)
	if err != nil {
		return nil, nil, err
	}
	var m *float64
	if mean.Valid {
		m = ptr(mean.Float64)
	}
	return qs, m, nil
}

// floats reads a DOUBLE list. A NULL list (no rows) is empty.
func floats(v any) ([]float64, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("quantile list has type %T", v)
	}
	out := make([]float64, len(list))
	for i, e := range list {
		f, ok := e.(float64)
		if !ok {
			return nil, fmt.Errorf("quantile has type %T", e)
		}
		out[i] = f
	}
	return out, nil
}

func (b *builder) syntheticStats(ctx context.Context, conn *sql.Conn, target, x, snapType string) (*Stats, error) {
	distinct := "count(DISTINCT " + x + ")"
	if profile.KindOf(snapType) == profile.KindOther {
		distinct = "0"
	}
	var rows, nonNull int64
	s := &Stats{}
	q := "SELECT count(*), count(" + x + "), " + distinct + " FROM " + target
	if err := conn.QueryRowContext(ctx, q).Scan(&rows, &nonNull, &s.Distinct); err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	if rows > 0 {
		s.NullRate = float64(rows-nonNull) / float64(rows)
	}
	e := profile.Axis(snapType, x)
	if e == "" || nonNull == 0 {
		return s, nil
	}
	q = "SELECT min(" + e + "), max(" + e + "), quantile_cont(" + e + ", 0.5), avg(" + e + " ORDER BY " + e + ") FROM " + target +
		" WHERE isfinite(" + e + ")"
	var mn, mx, med, mean sql.NullFloat64
	if err := conn.QueryRowContext(ctx, q).Scan(&mn, &mx, &med, &mean); err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	for _, f := range []struct {
		dst **float64
		v   sql.NullFloat64
	}{{&s.Min, mn}, {&s.Max, mx}, {&s.Median, med}, {&s.Mean, mean}} {
		if f.v.Valid {
			*f.dst = ptr(f.v.Float64)
		}
	}
	return s, nil
}

// distance compares the synthetic column with the filtered profile: ks
// for a column drawn from the quantile grid (on the offset axis for
// dateOffset), tv for one drawn from the kept values. Other generators
// get none.
func (b *builder) distance(ctx context.Context, conn *sql.Conn, target, x string, c *column) (*Distance, error) {
	p := c.prof
	e := profile.Axis(c.snapType, x)
	switch c.choice.Generator {
	case apitypes.GenCategorical, apitypes.GenUniformCategory, apitypes.GenShuffleLabels, apitypes.GenBoolean:
		if len(p.TopValues.Values) > 0 {
			return b.totalVariation(ctx, conn, target, x, p)
		}
	case apitypes.GenBucket:
	case apitypes.GenForeignKey:
		if len(p.FanOut) > 0 {
			return b.fanOutTV(ctx, conn, target, c)
		}
	case apitypes.GenDateOffset:
		// The profile is on the offset axis, so the distance is too.
		if len(c.refs) == 1 && len(p.Quantiles) >= 2 {
			f := parser.QuoteIdent(c.sibs[c.refs[0]].Name)
			return b.ks(ctx, conn, target, profile.OffsetAxis(c.snapType, x, f), p.Quantiles)
		}
	default:
		if len(p.Quantiles) >= 2 && e != "" {
			return b.ks(ctx, conn, target, e, p.Quantiles)
		}
	}
	return nil, nil
}

// ks is max_i |p_i - F_syn(q_i)| on the grid p_i = i/S of the profile
// quantiles q_i. F_syn interpolates the synthetic quantiles at the same
// p_i linearly.
func (b *builder) ks(ctx context.Context, conn *sql.Conn, target, e string, q []float64) (*Distance, error) {
	s := len(q) - 1
	probs := make([]float64, len(q))
	for i := range probs {
		probs[i] = float64(i) / float64(s)
	}
	syn, _, err := b.quantilesMean(ctx, conn, e, target, probs)
	if err != nil || len(syn) != len(q) {
		return nil, err
	}
	var d float64
	for i, qi := range q {
		d = math.Max(d, math.Abs(probs[i]-cdf(syn, probs, qi)))
	}
	return &Distance{Metric: "ks", Value: d}, nil
}

// cdf returns the share of values at or below v, interpolated between
// the quantiles qs at probs.
func cdf(qs, probs []float64, v float64) float64 {
	if v < qs[0] {
		return 0
	}
	if v >= qs[len(qs)-1] {
		return 1
	}
	for j := 0; j+1 < len(qs); j++ {
		if v < qs[j+1] {
			if qs[j+1] == qs[j] {
				return probs[j+1]
			}
			return probs[j] + float64((v-qs[j])/(qs[j+1]-qs[j])*(probs[j+1]-probs[j]))
		}
	}
	return 1
}

// totalVariation is 0.5 * sum |share_src - share_syn| over the kept
// values plus one bucket for the rest.
func (b *builder) totalVariation(ctx context.Context, conn *sql.Conn, target, x string, p *profile.Column) (*Distance, error) {
	rows, err := conn.QueryContext(ctx, "SELECT "+x+", count(*) FROM "+target+" WHERE "+x+" IS NOT NULL GROUP BY 1")
	if err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	defer func() { _ = rows.Close() }()
	syn := map[string]int64{}
	var synTotal int64
	for rows.Next() {
		var v any
		var n int64
		if err := rows.Scan(&v, &n); err != nil {
			return nil, b.duckErr("read the report statistics", err)
		}
		syn[fmt.Sprint(v)] += n
		synTotal += n
	}
	if err := rows.Err(); err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	srcTotal := p.TopValues.Rest
	for _, v := range p.TopValues.Values {
		srcTotal += v.Count
	}
	if srcTotal == 0 || synTotal == 0 {
		return nil, nil
	}
	var sum float64
	synKept := int64(0)
	for _, v := range p.TopValues.Values {
		n := syn[fmt.Sprint(v.V)]
		synKept += n
		sum += math.Abs(float64(v.Count)/float64(srcTotal) - float64(n)/float64(synTotal))
	}
	sum += math.Abs(float64(p.TopValues.Rest)/float64(srcTotal) - float64(synTotal-synKept)/float64(synTotal))
	return &Distance{Metric: "tv", Value: 0.5 * sum}, nil
}
