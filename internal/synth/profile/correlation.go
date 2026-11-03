// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
)

// ErrPairRows is the error of a pair with too few complete rows.
var ErrPairRows = errors.New("fewer than 2 x minGroupSize rows with both values")

// Correlation is the rank correlation of a correlated group.
type Correlation struct {
	// Rho is the symmetric Spearman matrix, 1 on the diagonal.
	Rho [][]float64
	// NoSpread lists the pairs (i < j) whose table has a margin without
	// spread. Their rho is 0.
	NoSpread [][2]int
}

// Correlation estimates the Spearman matrix of cols from one B_a x B_b
// table per pair (plan 1.2), all pairs in one scan. edges[i] are the
// inner cell edges of cols[i] (CellEdges), so len(edges[i]) + 1 is B_i.
// A pair needs 2k rows with both values finite. noise nil is off; with
// it the cells of pair (a, b) get the measurement corr/<b> of column a,
// clamped at 0 (plan 4.3).
func (r *Runner) Correlation(ctx context.Context, t Table, cols []ColumnInfo, edges [][]float64, k int64, noise func(column string) *Noise) (*Correlation, error) {
	m := len(cols)
	names := make([]string, m)
	for i, c := range cols {
		names[i] = c.Name
	}
	p := &colRun{ctx: ctx, r: r, name: strings.Join(names, ", ")}
	if m < 2 || len(edges) != m {
		return nil, p.fail(ErrKind)
	}
	sel := make([]string, m)
	ids := make([]string, m)
	var args []any
	for i, c := range cols {
		e := Axis(c.Type, parser.QuoteIdent(c.Name))
		if e == "" {
			return nil, fmt.Errorf("column %s: %w", c.Name, ErrKind)
		}
		var b strings.Builder
		b.WriteString("CASE WHEN " + e + " IS NULL OR NOT isfinite(" + e + ") THEN -1")
		for j, x := range edges[i] {
			b.WriteString(" WHEN " + e + " < ? THEN " + strconv.Itoa(j))
			args = append(args, x)
		}
		ids[i] = "b" + strconv.Itoa(i)
		sel[i] = b.String() + " ELSE " + strconv.Itoa(len(edges[i])) + " END AS " + ids[i]
	}
	// GROUPING_ID sets bit m-1-i when b_i is not in the grouping set, so
	// it names the pair of each result row.
	type pair struct{ a, b int }
	var pairs []pair
	var sets []string
	pairOf := map[int64]int{}
	for a := range m {
		for b := a + 1; b < m; b++ {
			full := int64(1)<<m - 1
			pairOf[full&^(int64(1)<<(m-1-a))&^(int64(1)<<(m-1-b))] = len(pairs)
			pairs = append(pairs, pair{a, b})
			sets = append(sets, "("+ids[a]+", "+ids[b]+")")
		}
	}
	tables := make([][][]int64, len(pairs))
	for i, pr := range pairs {
		tables[i] = make([][]int64, len(edges[pr.a])+1)
		for j := range tables[i] {
			tables[i][j] = make([]int64, len(edges[pr.b])+1)
		}
	}
	idList := strings.Join(ids, ", ")
	q := "WITH c AS (SELECT " + strings.Join(sel, ", ") + " FROM " + t.sql() + ") " +
		"SELECT GROUPING_ID(" + idList + "), " + idList + ", count(*) FROM c GROUP BY GROUPING SETS (" + strings.Join(sets, ", ") + ")"
	err := p.query(q, args, func(rows *sql.Rows) error {
		var id, n int64
		vals := make([]sql.NullInt64, m)
		dest := []any{&id}
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(append(dest, &n)...); err != nil {
			return err
		}
		pi, ok := pairOf[id]
		if !ok {
			return fmt.Errorf("unknown grouping id %d", id)
		}
		x, y := vals[pairs[pi].a], vals[pairs[pi].b]
		if !x.Valid || !y.Valid {
			return errors.New("pair cell is NULL")
		}
		if x.Int64 < 0 || y.Int64 < 0 {
			return nil
		}
		tables[pi][x.Int64][y.Int64] += n
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := &Correlation{Rho: make([][]float64, m)}
	for i := range out.Rho {
		out.Rho[i] = make([]float64, m)
		out.Rho[i][i] = 1
	}
	for i, pr := range pairs {
		if noise != nil {
			flat := make([]int64, 0, len(tables[i])*len(tables[i][0]))
			for _, row := range tables[i] {
				flat = append(flat, row...)
			}
			noisy, _ := noise(cols[pr.a].Name).cells("corr/"+cols[pr.b].Name, flat, 1)
			for j, row := range tables[i] {
				copy(row, noisy[j*len(row):])
			}
		}
		var total int64
		cells := make([][]float64, len(tables[i]))
		for j, row := range tables[i] {
			cells[j] = make([]float64, len(row))
			for l, n := range row {
				cells[j][l] = float64(n)
				total += n
			}
		}
		if total < 2*k {
			return nil, fmt.Errorf("column %s, %s: %w", cols[pr.a].Name, cols[pr.b].Name, ErrPairRows)
		}
		rho, ok := RankCorrelation(cells)
		if !ok {
			out.NoSpread = append(out.NoSpread, [2]int{pr.a, pr.b})
			rho = 0
		}
		out.Rho[pr.a][pr.b], out.Rho[pr.b][pr.a] = rho, rho
	}
	return out, nil
}

// RankCorrelation is Spearman of a rectangular contingency table: the
// weighted Pearson correlation of the midranks of the row and column
// margins. ok is false when a margin has no spread.
func RankCorrelation(cells [][]float64) (rho float64, ok bool) {
	if len(cells) == 0 || len(cells[0]) == 0 {
		return 0, false
	}
	rm := make([]float64, len(cells))
	cm := make([]float64, len(cells[0]))
	var n float64
	for i, row := range cells {
		for j, x := range row {
			rm[i] += x
			cm[j] += x
			n += x
		}
	}
	if !(n > 0) {
		return 0, false
	}
	mean := (n + 1) / 2
	rr, cr := midranks(rm, mean), midranks(cm, mean)
	var vr, vc, cov float64
	for i, d := range rr {
		vr += float64(rm[i] * float64(d*d))
	}
	for j, d := range cr {
		vc += float64(cm[j] * float64(d*d))
	}
	if !(vr > 0) || !(vc > 0) {
		return 0, false
	}
	for i, row := range cells {
		for j, x := range row {
			cov += float64(x * float64(rr[i]*cr[j]))
		}
	}
	return max(-1, min(1, cov/math.Sqrt(float64(vr*vc)))), true
}

// midranks returns the midrank of each margin cell minus mean.
func midranks(m []float64, mean float64) []float64 {
	out := make([]float64, len(m))
	var cum float64
	for i, x := range m {
		out[i] = cum + (x+1)/2 - mean
		cum += x
	}
	return out
}
