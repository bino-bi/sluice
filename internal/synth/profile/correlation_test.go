// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"testing"
)

// spearmanRows is the reference: Spearman of the rows a table expands to,
// with midranks per observation.
func spearmanRows(cells [][]float64) float64 {
	var xs, ys []float64
	for i, row := range cells {
		for j, n := range row {
			for range int(n) {
				xs = append(xs, float64(i))
				ys = append(ys, float64(j))
			}
		}
	}
	rank := func(v []float64) []float64 {
		idx := make([]int, len(v))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
		out := make([]float64, len(v))
		for s := 0; s < len(idx); {
			e := s
			for e < len(idx) && v[idx[e]] == v[idx[s]] {
				e++
			}
			for k := s; k < e; k++ {
				out[idx[k]] = float64(s+e+1) / 2
			}
			s = e
		}
		return out
	}
	rx, ry := rank(xs), rank(ys)
	n := float64(len(rx))
	var mx, my float64
	for i := range rx {
		mx += rx[i] / n
		my += ry[i] / n
	}
	var c, vx, vy float64
	for i := range rx {
		c += (rx[i] - mx) * (ry[i] - my)
		vx += (rx[i] - mx) * (rx[i] - mx)
		vy += (ry[i] - my) * (ry[i] - my)
	}
	return c / math.Sqrt(vx*vy)
}

// Test 7.
func TestRankCorrelation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cells [][]float64
		want  float64
	}{
		{"diagonal", [][]float64{{5, 0, 0}, {0, 7, 0}, {0, 0, 3}}, 1},
		{"anti-diagonal", [][]float64{{0, 0, 4}, {0, 6, 0}, {2, 0, 0}}, -1},
		{"uniform", [][]float64{{3, 3, 3, 3}, {3, 3, 3, 3}, {3, 3, 3, 3}, {3, 3, 3, 3}}, 0},
		// 2 x 2: Spearman is the phi coefficient.
		{"two by two", [][]float64{{10, 5}, {3, 12}}, (10*12 - 5*3) / math.Sqrt(15*15*13*17)},
		{"three by five", [][]float64{{4, 3, 1, 0, 2}, {1, 5, 6, 2, 0}, {0, 1, 3, 7, 5}}, spearmanRows([][]float64{{4, 3, 1, 0, 2}, {1, 5, 6, 2, 0}, {0, 1, 3, 7, 5}})},
	} {
		got, ok := RankCorrelation(tc.cells)
		if !ok || math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("%s: rho = %v, %v, want %v", tc.name, got, ok, tc.want)
		}
	}
	for name, cells := range map[string][][]float64{
		"one row cell": {{5, 0}, {0, 0}},
		"one column":   {{3}, {4}},
		"empty":        {{0, 0}, {0, 0}},
		"no rows":      {},
	} {
		if _, ok := RankCorrelation(cells); ok {
			t.Errorf("%s: ok, want no spread", name)
		}
	}
}

var people = Table{Catalog: "src", Schema: "crm", Name: "people"}

// peopleEdges profiles the named columns of crm.people and returns them
// with their cell edges.
func peopleEdges(t *testing.T, r *Runner, names ...string) ([]ColumnInfo, [][]float64) {
	t.Helper()
	ctx := context.Background()
	infos := make([]ColumnInfo, len(names))
	for i, n := range names {
		infos[i] = ColumnInfo{Name: n, Type: scalar[string](t, r,
			"SELECT data_type FROM information_schema.columns WHERE table_catalog = 'src' AND table_name = 'people' AND column_name = '"+n+"'")}
	}
	counts, err := r.Base(ctx, people, infos)
	if err != nil {
		t.Fatal(err)
	}
	edges := make([][]float64, len(names))
	for i, info := range infos {
		p, err := r.Column(ctx, people, info, counts[i], Needs{Parts: PartQuantiles}, Settings{MinGroupSize: 5, JitterOff: true}, jitterStream(i), nil)
		if err != nil {
			t.Fatalf("profile %s: %v", info.Name, err)
		}
		if p.Cells() != 20 {
			t.Fatalf("%s: cells = %d, want 20", info.Name, p.Cells())
		}
		edges[i] = p.CellEdges(p.Cells())
		if len(edges[i]) != 19 || !sort.Float64sAreSorted(edges[i]) {
			t.Fatalf("%s: edges not 19 sorted values", info.Name)
		}
	}
	return infos, edges
}

// Test 8.
func TestCorrelationQuery(t *testing.T) {
	r, _ := newRunner(t)
	ctx := context.Background()
	infos, edges := peopleEdges(t, r, "age", "income", "risk", "noise_col")
	run := func(threads string) [][]float64 {
		if _, err := r.Conn.ExecContext(ctx, "SET threads = "+threads); err != nil {
			t.Fatal(err)
		}
		c, err := r.Correlation(ctx, people, infos, edges, 5, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.NoSpread) != 0 {
			t.Fatalf("no spread: %v", c.NoSpread)
		}
		return c.Rho
	}
	rho := run("1")
	if rho[0][1] < 0.85 || rho[0][2] > -0.85 || math.Abs(rho[0][3]) >= 0.05 {
		t.Errorf("rho(age, income) %.3f, rho(age, risk) %.3f, rho(age, noise_col) %.3f", rho[0][1], rho[0][2], rho[0][3])
	}
	for i := range rho {
		if rho[i][i] != 1 {
			t.Errorf("diagonal %d = %v", i, rho[i][i])
		}
		for j := range rho {
			if rho[i][j] != rho[j][i] {
				t.Errorf("rho not symmetric at %d, %d", i, j)
			}
		}
	}
	four := run("4")
	for i := range rho {
		for j := range rho {
			if math.Float64bits(rho[i][j]) != math.Float64bits(four[i][j]) {
				t.Errorf("rho[%d][%d]: threads 1 %v, threads 4 %v", i, j, rho[i][j], four[i][j])
			}
		}
	}

	// B_a != B_b in both orders: a swapped table index panics or reads
	// the wrong cells, which a square table does not show.
	for _, e := range [][][]float64{{edges[0][:9], edges[1]}, {edges[0], edges[1][:9]}} {
		c, err := r.Correlation(ctx, people, infos[:2], e, 5, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Rho[0][1]; got < 0.8 || len(c.NoSpread) != 0 {
			t.Errorf("rectangular %d x %d: rho %.3f, no spread %v", len(e[0])+1, len(e[1])+1, got, c.NoSpread)
		}
	}

	_, err := r.Correlation(ctx, people, infos[:2], edges[:2], 10001, nil)
	if !errors.Is(err, ErrPairRows) || !strings.Contains(err.Error(), "column age, income:") || strings.Contains(err.Error(), "20000") {
		t.Errorf("err = %v", err)
	}
}

// A constant member gives no spread and rho 0.
func TestCorrelationNoSpread(t *testing.T) {
	r, _ := newRunner(t)
	ctx := context.Background()
	if _, err := r.Conn.ExecContext(ctx, "CREATE TABLE src.crm.flat AS SELECT CAST(i AS DOUBLE) AS a, 1.0 AS b FROM range(100) r(i)"); err != nil {
		t.Fatal(err)
	}
	infos := []ColumnInfo{{Name: "a", Type: "DOUBLE"}, {Name: "b", Type: "DOUBLE"}}
	c, err := r.Correlation(ctx, Table{Catalog: "src", Schema: "crm", Name: "flat"}, infos, [][]float64{{50}, {1}}, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Rho[0][1] != 0 || len(c.NoSpread) != 1 || c.NoSpread[0] != [2]int{0, 1} {
		t.Errorf("rho %v, no spread %v", c.Rho, c.NoSpread)
	}
}

// Correlation names the pair of a result row by GROUPING_ID: bit m-1-i
// is set when b_i is not in the grouping set.
func TestGroupingIDProbe(t *testing.T) {
	r, _ := newRunner(t)
	rows, err := r.Conn.QueryContext(context.Background(),
		"WITH c AS (SELECT 1 AS b0, 2 AS b1, 3 AS b2) SELECT GROUPING_ID(b0, b1, b2), b0, b1, b2 FROM c "+
			"GROUP BY GROUPING SETS ((b0, b1), (b0, b2), (b1, b2))")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[int64]bool{}
	for rows.Next() {
		var id int64
		var b0, b1, b2 any
		if err := rows.Scan(&id, &b0, &b1, &b2); err != nil {
			t.Fatal(err)
		}
		switch id {
		case 1:
			if b2 != nil {
				t.Errorf("id 1 has b2 %v", b2)
			}
		case 2:
			if b1 != nil {
				t.Errorf("id 2 has b1 %v", b1)
			}
		case 4:
			if b0 != nil {
				t.Errorf("id 4 has b0 %v", b0)
			}
		}
		got[id] = true
	}
	if len(got) != 3 || !got[1] || !got[2] || !got[4] {
		t.Errorf("grouping ids = %v, want 1, 2, 4", got)
	}
}
