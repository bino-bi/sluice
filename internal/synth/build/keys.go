// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// keyCache holds the staged values of the parent key columns a
// foreignKey reads (plan 1.8 step 1), by lower-case table and column
// name. A parent table is built before its children, so its keys are
// final when a child reads them.
type keyCache struct {
	// want lists the referenced columns before any table is built.
	want map[string]map[string]bool
	keys map[string]map[string]*keyEntry
}

// keyEntry is one parent key column after its table is built.
type keyEntry struct {
	set *generate.KeySet
	// table is the name as in the spec, column the source name.
	table, column string
	// unique is set when the parent generator gives unique values.
	unique bool
}

func newKeyCache(spec *apitypes.SyntheticSpec) *keyCache {
	k := &keyCache{want: map[string]map[string]bool{}, keys: map[string]map[string]*keyEntry{}}
	for _, t := range spec.Tables {
		for _, c := range t.Columns {
			if table, column, ok := c.ForeignKey(); ok {
				if k.want[table] == nil {
					k.want[table] = map[string]bool{}
				}
				k.want[table][column] = true
			}
		}
	}
	return k
}

func (k *keyCache) wanted(table, column string) bool {
	return k.want[strings.ToLower(table)][strings.ToLower(column)]
}

// put stores the keys of a built parent column. With no rows the choice
// is empty and the set too.
func (k *keyCache) put(table string, c *column) {
	low := strings.ToLower(table)
	if k.keys[low] == nil {
		k.keys[low] = map[string]*keyEntry{}
	}
	unique, _ := c.choice.Args["unique"].(bool)
	switch c.choice.Generator {
	case apitypes.GenSequence, apitypes.GenUUID:
		unique = true
	}
	c.keyOut.Stage = c.stage
	k.keys[low][strings.ToLower(c.info.Name)] = &keyEntry{set: c.keyOut, table: table, column: c.info.Name, unique: unique}
}

func (k *keyCache) get(table, column string) *keyEntry {
	return k.keys[table][column]
}

// tableDeps returns the parent tables of t as named in the spec, sorted.
func tableDeps(spec *apitypes.SyntheticSpec, t *apitypes.SyntheticTable) []string {
	names := map[string]string{}
	for _, st := range spec.Tables {
		names[strings.ToLower(st.Name)] = st.Name
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range t.Columns {
		if table, _, ok := c.ForeignKey(); ok && !seen[table] {
			seen[table] = true
			out = append(out, names[table])
		}
	}
	sort.Strings(out)
	return out
}

func foreignKeyOf(c *column) (table, column string, ok bool) {
	return apitypes.SyntheticColumn{Generator: c.choice.Generator, Args: c.choice.Args}.ForeignKey()
}

// checkForeignKeys checks the parent of every foreignKey column of a
// table (plan 1.8 steps 1 and 2) and hands it its keys.
func (b *builder) checkForeignKeys(cols []*column) []error {
	var errs []error
	for _, c := range cols {
		table, column, ok := foreignKeyOf(c)
		if !ok {
			continue
		}
		e := b.keys.get(table, column)
		var err error
		switch {
		case e == nil:
			err = fmt.Errorf("column %s: foreignKey names column %s, which is not in %s", c.info.Name, c.choice.Args["column"], c.choice.Args["table"])
		case len(e.set.Values) == 0:
			err = fmt.Errorf("column %s: foreignKey parent %s has no keys", c.info.Name, e.table)
		case !e.unique:
			err = fmt.Errorf("column %s: foreignKey needs unique values in %s.%s (sequence, uuid or unique: true)", c.info.Name, e.table, e.column)
		case e.set.Stage != generate.StageOf(generate.Column{Type: c.info.Type}, apitypes.GenForeignKey):
			err = fmt.Errorf("column %s: foreignKey type does not match %s.%s", c.info.Name, e.table, e.column)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.keys = e.set
	}
	return errs
}

// parentTarget returns the snapshot table and the quoted key column of
// the parent of a foreignKey column.
func (b *builder) parentTarget(c *column) (*keyEntry, string, string) {
	table, column, _ := foreignKeyOf(c)
	e := b.keys.get(table, column)
	schema, name, _ := strings.Cut(e.table, ".")
	return e, outCatalog + "." + parser.QuoteIdent(schema) + "." + parser.QuoteIdent(name), parser.QuoteIdent(e.column)
}

// verifyForeignKeys counts the keys of the written table with no parent
// (plan 1.8 step 7). The count must equal the orphans drawn.
func (b *builder) verifyForeignKeys(ctx context.Context, conn *sql.Conn, target string, cols []*column) error {
	for _, c := range cols {
		if _, _, ok := foreignKeyOf(c); !ok || c.gen == nil {
			continue
		}
		_, parent, pk := b.parentTarget(c)
		x := parser.QuoteIdent(c.info.Name)
		q := "SELECT count(*) FROM " + target + " AS c WHERE c." + x + " IS NOT NULL AND NOT EXISTS (SELECT 1 FROM " +
			parent + " AS p WHERE p." + pk + " = c." + x + ")"
		var n int64
		if err := conn.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return b.duckErr("check a foreignKey", err)
		}
		var want int64
		if f, ok := c.gen.(generate.FanOuter); ok && f.FanOutStats() != nil {
			want = f.FanOutStats().Orphans
		}
		if n != want {
			return fmt.Errorf("foreignKey check failed for column %s", c.info.Name)
		}
	}
	return nil
}

// fanOutTV is the tv distance between the filtered source fan-out
// histogram and the fan-outs of the snapshot, parents with no child
// included.
func (b *builder) fanOutTV(ctx context.Context, conn *sql.Conn, target string, c *column) (*Distance, error) {
	_, parent, pk := b.parentTarget(c)
	x := parser.QuoteIdent(c.info.Name)
	q := "WITH k AS (SELECT DISTINCT " + pk + " AS p FROM " + parent + " WHERE " + pk + " IS NOT NULL), " +
		"c AS (SELECT " + x + " AS fk, count(*) AS n FROM " + target + " WHERE " + x + " IS NOT NULL GROUP BY 1) " +
		"SELECT coalesce(c.n, 0) AS f, count(*) FROM k LEFT JOIN c ON c.fk = k.p GROUP BY f"
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	defer func() { _ = rows.Close() }()
	syn := map[int64]float64{}
	var synTotal float64
	for rows.Next() {
		var f, n int64
		if err := rows.Scan(&f, &n); err != nil {
			return nil, b.duckErr("read the report statistics", err)
		}
		syn[f] += float64(n)
		synTotal += float64(n)
	}
	if err := rows.Err(); err != nil {
		return nil, b.duckErr("read the report statistics", err)
	}
	src := map[int64]float64{}
	var srcTotal float64
	for _, bin := range c.prof.FanOut {
		src[int64(bin.Lo)] += float64(bin.Count)
		srcTotal += float64(bin.Count)
	}
	if srcTotal == 0 || synTotal == 0 {
		return nil, nil
	}
	keys := map[int64]bool{}
	for f := range src {
		keys[f] = true
	}
	for f := range syn {
		keys[f] = true
	}
	sorted := make([]int64, 0, len(keys))
	for f := range keys {
		sorted = append(sorted, f)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum float64
	for _, f := range sorted {
		d := src[f]/srcTotal - syn[f]/synTotal
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return &Distance{Metric: "tv", Value: 0.5 * sum}, nil
}
