// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/rng"
)

// MaxGroups caps the by-groups of a column. Every group costs a set of
// profile queries.
const MaxGroups = 100

// groupTable holds the rows of one group while it is profiled. The
// builder's default catalog is memory.
var groupTable = Table{Catalog: "memory", Schema: "main", Name: "sluice_group"}

// Group is the profile of one by-group. Key is the length-prefixed list
// of the group's by values as text, a NULL value marked apart.
type Group struct {
	Key     string
	Profile *Column
}

// GroupStats are the counts for the report.
type GroupStats struct {
	Kept, Dropped int
	// SpikeGroups is the number of kept groups with a spike.
	SpikeGroups int
}

// GroupKey encodes the by values of one group: "n" for NULL, else the
// byte length, ":" and the text. No two lists give the same key.
func GroupKey(vals []sql.NullString) string {
	var b strings.Builder
	for _, v := range vals {
		if !v.Valid {
			b.WriteByte('n')
			continue
		}
		b.WriteString(strconv.Itoa(len(v.String)))
		b.WriteByte(':')
		b.WriteString(v.String)
	}
	return b.String()
}

// Groups profiles col per group of by. needs and s are the column's;
// the spike check runs per group. jitter returns the jitter stream of a
// group. Groups are the MaxGroups largest with at least k non-null rows
// of col, largest first, ties by key text with NULL first. A group whose
// profile fails the filter is dropped; its rows use the global profile.
func (r *Runner) Groups(ctx context.Context, t Table, col ColumnInfo, by []ColumnInfo,
	needs Needs, s Settings, jitter func(key string) *rng.Stream) ([]Group, GroupStats, error) {
	k := int64(s.MinGroupSize)
	p := &colRun{ctx: ctx, r: r, name: col.Name}
	x := parser.QuoteIdent(col.Name)
	sel := make([]string, len(by))
	pos := make([]string, len(by))
	ord := make([]string, len(by))
	where := make([]string, len(by))
	for i, b := range by {
		sel[i] = "CAST(" + parser.QuoteIdent(b.Name) + " AS VARCHAR)"
		pos[i] = strconv.Itoa(i + 1)
		ord[i] = pos[i] + " NULLS FIRST"
		where[i] = sel[i] + " IS NOT DISTINCT FROM ?"
	}
	// The window counts the groups left after HAVING, before LIMIT.
	q := "SELECT " + strings.Join(sel, ", ") + ", count(" + x + ") AS n, count(*) OVER () FROM " + t.sql() +
		" GROUP BY " + strings.Join(pos, ", ") + " HAVING count(" + x + ") >= ?" +
		" ORDER BY n DESC, " + strings.Join(ord, ", ") + " LIMIT " + strconv.Itoa(MaxGroups)
	var keys [][]sql.NullString
	var total int64
	err := p.query(q, []any{k}, func(rows *sql.Rows) error {
		vals := make([]sql.NullString, len(by))
		dest := make([]any, 0, len(by)+2)
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		var n int64
		if err := rows.Scan(append(dest, &n, &total)...); err != nil {
			return err
		}
		keys = append(keys, vals)
		return nil
	})
	if err != nil {
		return nil, GroupStats{}, err
	}
	stats := GroupStats{Dropped: int(total) - len(keys)}
	if len(keys) == 0 {
		return nil, stats, nil
	}
	defer func() {
		_, _ = r.Conn.ExecContext(context.WithoutCancel(ctx), "DROP TABLE IF EXISTS "+groupTable.sql())
	}()
	create := "CREATE OR REPLACE TABLE " + groupTable.sql() + " AS SELECT " + x + " FROM " + t.sql() +
		" WHERE " + strings.Join(where, " AND ")
	var out []Group
	for _, vals := range keys {
		args := make([]any, len(vals))
		for i, v := range vals {
			args[i] = v
		}
		if _, err := r.Conn.ExecContext(ctx, create, args...); err != nil {
			r.logger().Debug("synthetic group table failed", "column", col.Name, "error", err)
			return nil, stats, p.fail(ErrQuery)
		}
		counts, err := r.Base(ctx, groupTable, []ColumnInfo{col})
		if err != nil {
			return nil, stats, p.fail(ErrQuery)
		}
		key := GroupKey(vals)
		prof, err := r.Column(ctx, groupTable, col, counts[0], needs, s, jitter(key), nil)
		if errors.Is(err, ErrTooFewRows) || errors.Is(err, ErrGrid) || errors.Is(err, ErrNoGroups) {
			stats.Dropped++
			continue
		}
		if err != nil {
			return nil, stats, err
		}
		if len(prof.Spikes) > 0 {
			stats.SpikeGroups++
		}
		out = append(out, Group{Key: key, Profile: prof})
	}
	stats.Kept = len(out)
	return out, stats, nil
}
