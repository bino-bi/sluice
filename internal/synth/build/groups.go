// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/synth/generate"
	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// corrGroup is one correlated group of a table.
type corrGroup struct {
	// members are indexes into the table's columns, in ordinal order.
	members []int
	// k is the largest minGroupSize of the members.
	k   int64
	rep CorrelationReport
}

// correlatedGroups returns the groups of a table: member indexes into
// cols, in ordinal order, groups ordered by their first member. with
// edges are undirected.
func correlatedGroups(cols []*column) [][]int {
	idx := map[string]int{}
	parent := make([]int, len(cols))
	for i, c := range cols {
		parent[i] = i
		if c.choice.Generator == apitypes.GenCorrelated {
			idx[strings.ToLower(c.info.Name)] = i
		}
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for i, c := range cols {
		if c.choice.Generator != apitypes.GenCorrelated {
			continue
		}
		list, _ := c.choice.Args["with"].([]any)
		for _, e := range list {
			w, _ := e.(string)
			if j, ok := idx[strings.ToLower(w)]; ok {
				parent[find(i)] = find(j)
			}
		}
	}
	var groups [][]int
	pos := map[int]int{}
	for i, c := range cols {
		if c.choice.Generator != apitypes.GenCorrelated {
			continue
		}
		r := find(i)
		p, ok := pos[r]
		if !ok {
			p = len(groups)
			pos[r] = p
			groups = append(groups, nil)
		}
		groups[p] = append(groups[p], i)
	}
	return groups
}

// prepareGroup profiles the members, estimates the correlation, builds
// the copula and the value sources. Correlated is never a default, so a
// failure fails the build.
func (b *builder) prepareGroup(ctx context.Context, conn *sql.Conn, runner *profile.Runner, t *apitypes.SyntheticTable,
	pt profile.Table, cols []*column, members []int, rows int64) (*corrGroup, error) {
	g, _ := generate.Lookup(apitypes.GenCorrelated)
	m := len(members)
	grp := &corrGroup{members: members, rep: CorrelationReport{Columns: make([]string, m), Cells: make([]int, m)}}
	infos := make([]profile.ColumnInfo, m)
	edges := make([][]float64, m)
	gcols := make([]generate.Column, m)
	for j, i := range members {
		c := cols[i]
		gcols[j] = b.genColumn(ctx, conn, c, rows)
		settings := profile.Effective(c.choice.Privacy, t.Privacy, b.spec.Privacy)
		needs := g.Needs(gcols[j], c.choice.Args)
		prof, err := runner.Column(ctx, pt, c.info, c.counts, needs, settings,
			rng.For(b.seed, b.name, t.Name, c.info.Name, rng.PurposeJitter), b.columnNoise(t, c.info.Name))
		if err != nil {
			return nil, err
		}
		c.settings, c.prof, c.needs = settings, prof, needs
		grp.rep.Columns[j] = c.info.Name
		grp.rep.Cells[j] = prof.Cells()
		edges[j] = prof.CellEdges(grp.rep.Cells[j])
		infos[j] = c.info
		grp.k = max(grp.k, int64(settings.MinGroupSize))
	}
	corr, err := runner.Correlation(ctx, pt, infos, edges, grp.k, b.noiseFor(t))
	if err != nil {
		return nil, err
	}
	for _, p := range corr.NoSpread {
		w := fmt.Sprintf("column %s, %s: no rank spread, correlation set to 0", infos[p[0]].Name, infos[p[1]].Name)
		b.rep.Warnings = append(b.rep.Warnings, t.Name+": "+w)
		b.log.Warn("synthetic build: "+w, slog.String("name", b.name), slog.String("table", t.Name))
	}
	copula, shrinkage, err := generate.NewCopula(corr.Rho,
		rng.For(b.seed, b.name, t.Name, cols[members[0]].info.Name, rng.PurposeCopula))
	if err != nil {
		return nil, fmt.Errorf("columns %s: %w", strings.Join(grp.rep.Columns, ", "), err)
	}
	grp.rep.Shrinkage, grp.rep.Source = shrinkage, corr.Rho
	for j, i := range members {
		c := cols[i]
		stream := func(purpose string) *rng.Stream { return rng.For(b.seed, b.name, t.Name, c.info.Name, purpose) }
		gen, err := generate.New(apitypes.GenCorrelated, gcols[j], c.prof, c.choice.Args, generate.Streams{
			Values:  stream(rng.PurposeValues),
			Nulls:   stream(rng.PurposeNulls),
			Shuffle: stream(rng.PurposeShuffle),
			Copula:  copula.Slot(j),
		})
		if err != nil {
			return nil, err
		}
		c.gen = gen
		c.stage = generate.StageOf(gcols[j], apitypes.GenCorrelated)
		c.snapType = snapshotType(c.info.Type, apitypes.GenCorrelated)
	}
	return grp, nil
}

// syntheticCorrelation measures the group on the snapshot table with
// cell edges from the snapshot itself. Too few complete rows or no spread
// leave Synthetic empty with a warning; they never fail the build.
func (b *builder) syntheticCorrelation(ctx context.Context, conn *sql.Conn, runner *profile.Runner, t *apitypes.SyntheticTable,
	schema, name string, cols []*column, grp *corrGroup) error {
	target := profile.Table{Catalog: outCatalog, Schema: schema, Name: name}
	from := outCatalog + "." + parser.QuoteIdent(schema) + "." + parser.QuoteIdent(name)
	m := len(grp.members)
	infos := make([]profile.ColumnInfo, m)
	edges := make([][]float64, m)
	measured := true
	for j, i := range grp.members {
		c := cols[i]
		infos[j] = profile.ColumnInfo{Name: c.info.Name, Type: c.snapType}
		n := grp.rep.Cells[j]
		probs := make([]float64, n-1)
		for l := range probs {
			probs[l] = float64(l+1) / float64(n)
		}
		qs, _, err := b.quantilesMean(ctx, conn, profile.Axis(c.snapType, parser.QuoteIdent(c.info.Name)), from, probs)
		if err != nil {
			return err
		}
		edges[j] = qs
		measured = measured && len(qs) == len(probs)
	}
	var corr *profile.Correlation
	if measured {
		var err error
		corr, err = runner.Correlation(ctx, target, infos, edges, grp.k, nil)
		switch {
		case errors.Is(err, profile.ErrPairRows):
			measured = false
		case err != nil:
			return err
		default:
			measured = len(corr.NoSpread) == 0
		}
	}
	if !measured {
		b.rep.Warnings = append(b.rep.Warnings, fmt.Sprintf("%s: correlation of %s not measured on the snapshot", t.Name, strings.Join(grp.rep.Columns, ", ")))
		return nil
	}
	grp.rep.Synthetic = corr.Rho
	for i := range corr.Rho {
		for j := range i {
			grp.rep.MaxDiff = math.Max(grp.rep.MaxDiff, math.Abs(grp.rep.Source[i][j]-corr.Rho[i][j]))
		}
	}
	return nil
}
