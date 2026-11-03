// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

const (
	// rnColumn holds the row index in the staging table of a table with
	// rejectExactMatches.
	rnColumn = "sluice_rn"
	// maxRedrawRounds bounds the redraws of rejectExactMatches.
	maxRedrawRounds = 10
)

// exactColumns returns the ordinal positions of the columns in the
// table's privacy.rejectExactMatches, in list order.
func exactColumns(t *apitypes.SyntheticTable, infos []profile.ColumnInfo) ([]int, error) {
	if t.Privacy == nil || len(t.Privacy.RejectExactMatches) == 0 {
		return nil, nil
	}
	pos := make(map[string]int, len(infos))
	for i, c := range infos {
		pos[strings.ToLower(c.Name)] = i
	}
	out := make([]int, 0, len(t.Privacy.RejectExactMatches))
	for _, n := range t.Privacy.RejectExactMatches {
		i, ok := pos[strings.ToLower(n)]
		if !ok {
			return nil, fmt.Errorf("privacy.rejectExactMatches names column %s, which is not in the table", n)
		}
		out = append(out, i)
	}
	return out, nil
}

// rejectExact finds the staged rows whose listed columns equal a source
// row and draws them again (plan 1.13). Source rows stay in DuckDB: only
// row indexes reach Go.
func (b *builder) rejectExact(ctx context.Context, conn *sql.Conn, pt profile.Table, cols []*column,
	order, list []int, rows int64) (*ExactMatchReport, error) {
	names := make([]string, len(list))
	for i, j := range list {
		if cols[j].choice.Generator == apitypes.GenExpression {
			return nil, fmt.Errorf("privacy.rejectExactMatches names expression column %s", cols[j].info.Name)
		}
		names[i] = cols[j].info.Name
	}
	set := redrawSet(cols, order, list)
	match := func(only []int64) ([]int64, error) {
		return b.exactMatches(ctx, conn, pt, cols, list, only)
	}
	first, err := match(nil)
	if err != nil {
		return nil, err
	}
	rep, err := redrawRounds(names, first, rows, match, func(rns []int64, round int) error {
		return b.redraw(ctx, conn, cols, set, rns, round)
	})
	if err != nil || rep.Rounds == 0 {
		return rep, err
	}
	for _, j := range set {
		if cols[j].keyOut != nil {
			if err := b.reloadKeys(ctx, conn, cols[j], j); err != nil {
				return nil, err
			}
		}
	}
	return rep, nil
}

// redrawRounds runs the 1 % rule and the redraw rounds. first are the
// rows that match after the main pass. match checks the given rows again,
// redraw draws them again.
func redrawRounds(names []string, first []int64, rows int64,
	match func(only []int64) ([]int64, error), redraw func(rns []int64, round int) error) (*ExactMatchReport, error) {
	rep := &ExactMatchReport{Columns: names, MatchedRows: int64(len(first))}
	if rep.MatchedRows*100 > rows {
		return nil, fmt.Errorf("%d of %d rows equal a source row on %s (more than 1 %%), the columns are too predictable together",
			rep.MatchedRows, rows, strings.Join(names, ", "))
	}
	cur := first
	for len(cur) > 0 {
		if rep.Rounds == maxRedrawRounds {
			return nil, fmt.Errorf("%d rows still equal a source row after %d redraws", len(cur), maxRedrawRounds)
		}
		rep.Rounds++
		if err := redraw(cur, rep.Rounds); err != nil {
			return nil, err
		}
		rep.RedrawnRows += int64(len(cur))
		var err error
		if cur, err = match(cur); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// redrawSet returns the listed columns and every column that reads one of
// them, directly or not, in evaluation order.
func redrawSet(cols []*column, order, list []int) []int {
	in := map[string]bool{}
	for _, j := range list {
		in[strings.ToLower(cols[j].info.Name)] = true
	}
	var out []int
	for _, j := range order {
		c := cols[j]
		hit := in[strings.ToLower(c.info.Name)]
		for _, r := range c.refs {
			hit = hit || in[r]
		}
		if hit {
			in[strings.ToLower(c.info.Name)] = true
			out = append(out, j)
		}
	}
	return out
}

// exactMatches returns the row indexes of the staged rows whose listed
// columns equal a source row, NULL equal to NULL, compared in the
// snapshot types. With only set it checks those rows only.
func (b *builder) exactMatches(ctx context.Context, conn *sql.Conn, pt profile.Table, cols []*column,
	list []int, only []int64) ([]int64, error) {
	conds := make([]string, len(list))
	for i, j := range list {
		c := cols[j]
		conds[i] = castExpr("s."+stageName(j), c.snapType) + " IS NOT DISTINCT FROM CAST(t." +
			parser.QuoteIdent(c.info.Name) + " AS " + c.snapType + ")"
	}
	src := parser.QuoteIdent(pt.Catalog) + "." + parser.QuoteIdent(pt.Schema) + "." + parser.QuoteIdent(pt.Name)
	q := "SELECT s." + rnColumn + " FROM " + stageTable + " AS s WHERE EXISTS (SELECT 1 FROM " + src +
		" AS t WHERE " + strings.Join(conds, " AND ") + ")"
	if only != nil {
		q += " AND s." + rnColumn + " IN (" + intList(only) + ")"
	}
	q += " ORDER BY s." + rnColumn
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A staged value that does not cast fails here first; name it.
		if cerr := b.castFailure(ctx, conn, cols); cerr != nil {
			return nil, cerr
		}
		return nil, b.duckErr("check exact matches", err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var rn int64
		if err := rows.Scan(&rn); err != nil {
			return nil, b.duckErr("check exact matches", err)
		}
		out = append(out, rn)
	}
	if err := rows.Err(); err != nil {
		return nil, b.duckErr("check exact matches", err)
	}
	return out, nil
}

// redraw draws the columns of set again for the staged rows rns and
// writes them back. The streams go on from where the main pass ended, so
// the result is fixed by the seed. A foreignKey column keeps its value:
// its slot is fixed by the row, and a new orphan draw would break the
// orphan count of the check after the write.
func (b *builder) redraw(ctx context.Context, conn *sql.Conn, cols []*column, set []int, rns []int64, round int) error {
	sel := make([]string, len(cols))
	for i := range cols {
		sel[i] = stageName(i)
	}
	q := "SELECT " + rnColumn + ", " + strings.Join(sel, ", ") + " FROM " + stageTable +
		" WHERE " + rnColumn + " IN (" + intList(rns) + ") ORDER BY " + rnColumn
	staged, err := b.readStaged(ctx, conn, q, len(cols))
	if err != nil {
		return err
	}
	var upd []int
	for _, j := range set {
		if _, _, fk := foreignKeyOf(cols[j]); !fk {
			upd = append(upd, j)
		}
	}
	if len(upd) == 0 {
		return nil
	}
	assign := make([]string, len(upd))
	for i, j := range upd {
		assign[i] = stageName(j) + " = ?"
	}
	stmt := "UPDATE " + stageTable + " SET " + strings.Join(assign, ", ") + " WHERE " + rnColumn + " = ?"
	args := make([]any, len(upd)+1)
	for _, r := range staged {
		row := generate.RowCtx{Index: r.rn, Values: r.vals}
		for i, j := range upd {
			v, err := cols[j].gen.Next(row)
			if err != nil {
				return err
			}
			row.Values[j] = v
			args[i] = v
			hashString(b.hash, "redraw")
			for _, x := range []any{int64(round), r.rn, int64(j), v} {
				if err := hashValue(b.hash, x); err != nil {
					return fmt.Errorf("column %s: %w", cols[j].info.Name, err)
				}
			}
		}
		args[len(upd)] = r.rn
		if _, err := conn.ExecContext(ctx, stmt, args...); err != nil {
			return b.duckErr("write a redrawn row", err)
		}
	}
	return nil
}

type stagedRow struct {
	rn   int64
	vals []any
}

// readStaged reads staged rows as rn, c0, c1, ... in the stage Go types.
// All rows are read before any update, as the instance has one connection.
func (b *builder) readStaged(ctx context.Context, conn *sql.Conn, q string, ncols int) ([]stagedRow, error) {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, b.duckErr("read the staged rows", err)
	}
	defer func() { _ = rows.Close() }()
	var out []stagedRow
	for rows.Next() {
		r := stagedRow{vals: make([]any, ncols)}
		ptrs := make([]any, ncols+1)
		ptrs[0] = &r.rn
		for i := range r.vals {
			ptrs[i+1] = &r.vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, b.duckErr("read the staged rows", err)
		}
		for i, v := range r.vals {
			if t, ok := v.(time.Time); ok {
				r.vals[i] = t.UTC()
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, b.duckErr("read the staged rows", err)
	}
	return out, nil
}

// reloadKeys reads the keys of a redrawn parent key column again, in row
// order, so a child table sees the final keys.
func (b *builder) reloadKeys(ctx context.Context, conn *sql.Conn, c *column, j int) error {
	q := "SELECT " + stageName(j) + " FROM " + stageTable + " WHERE " + stageName(j) + " IS NOT NULL ORDER BY " + rnColumn
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return b.duckErr("read the parent keys", err)
	}
	defer func() { _ = rows.Close() }()
	vals := make([]any, 0, len(c.keyOut.Values))
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return b.duckErr("read the parent keys", err)
		}
		if t, ok := v.(time.Time); ok {
			v = t.UTC()
		}
		vals = append(vals, v)
	}
	if err := rows.Err(); err != nil {
		return b.duckErr("read the parent keys", err)
	}
	c.keyOut.Values = vals
	return nil
}

func intList(v []int64) string {
	s := make([]string, len(v))
	for i, x := range v {
		s[i] = strconv.FormatInt(x, 10)
	}
	return strings.Join(s, ", ")
}
