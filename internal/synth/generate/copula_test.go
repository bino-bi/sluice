// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func copulaStream() *rng.Stream {
	return rng.For(testSeed, "syn", "crm.t", "a", rng.PurposeCopula)
}

func TestCholeskyKnown(t *testing.T) {
	a := [][]float64{{4, 12, -16}, {12, 37, -43}, {-16, -43, 98}}
	want := [][]float64{{2, 0, 0}, {6, 1, 0}, {-8, 5, 3}}
	l, ok := cholesky(a)
	if !ok {
		t.Fatal("cholesky failed")
	}
	for i := range want {
		for j := range want[i] {
			if math.Abs(l[i][j]-want[i][j]) > 1e-12 {
				t.Errorf("L[%d][%d] = %v, want %v", i, j, l[i][j], want[i][j])
			}
		}
	}
	if _, ok := cholesky([][]float64{{1, 2}, {2, 1}}); ok {
		t.Error("cholesky of an indefinite matrix succeeded")
	}
}

func TestNewCopulaShrinkage(t *testing.T) {
	_, l, err := NewCopula([][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}, copulaStream())
	if err != nil || l != 0 {
		t.Errorf("identity: shrinkage %v, err %v", l, err)
	}
	half, l, err := NewCopula([][]float64{{1, 0.5}, {0.5, 1}}, copulaStream())
	if err != nil || l != 0 {
		t.Fatalf("0.5: shrinkage %v, err %v", l, err)
	}
	// Spearman 0.5 is Pearson 2 sin(pi/12) of the normals; with unit
	// diagonal L[1][0] is that Pearson value.
	if got := half.l[1][0]; math.Abs(got-0.5176380902050415) > 1e-9 {
		t.Errorf("0.5: Pearson %v, want 0.5176380902", got)
	}
	c, l, err := NewCopula([][]float64{{1, .9, -.9}, {.9, 1, .9}, {-.9, .9, 1}}, copulaStream())
	if err != nil {
		t.Fatalf("non-PSD: %v", err)
	}
	if l <= 0 || l >= 1 {
		t.Errorf("non-PSD: shrinkage %v, want in (0, 1)", l)
	}
	for row := range int64(100) {
		for j := range 3 {
			u, err := c.Slot(j).U(row)
			if err != nil || !(u >= 0 && u < 1) {
				t.Fatalf("row %d member %d: u %v, err %v", row, j, u, err)
			}
		}
	}
}

func TestNewCopulaBadInput(t *testing.T) {
	for name, rho := range map[string][][]float64{
		"empty":         {},
		"not square":    {{1, 0.3}, {0.3}},
		"diagonal":      {{0.7, 0.3}, {0.3, 1}},
		"not symmetric": {{1, 0.3}, {0.4, 1}},
		"out of range":  {{1, 1.5}, {1.5, 1}},
		"nan":           {{1, math.NaN()}, {math.NaN(), 1}},
	} {
		_, _, err := NewCopula(rho, copulaStream())
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, v := range []string{"0.3", "0.4", "0.7", "1.5", "NaN"} {
			if strings.Contains(err.Error(), v) {
				t.Errorf("%s: error holds a value: %v", name, err)
			}
		}
	}
}

func TestNormCDF(t *testing.T) {
	for z, want := range map[float64]float64{
		-3: 0.0013498980316300946,
		-1: 0.15865525393145707,
		0:  0.5,
		1:  0.8413447460685429,
		3:  0.9986501019683699,
	} {
		if got := normCDF(z); math.Abs(got-want) > 1e-12 {
			t.Errorf("normCDF(%v) = %v, want %v", z, got, want)
		}
	}
	if u := normU(40); u >= 1 {
		t.Errorf("normU(40) = %v, want below 1", u)
	}
	if u := normU(-40); u < 0 {
		t.Errorf("normU(-40) = %v", u)
	}
}

// corrPair returns the value sources of two correlated DOUBLE columns
// with Spearman rho on numberProfile.
func corrPair(t *testing.T, rho float64) (ColumnGen, ColumnGen) {
	t.Helper()
	c, _, err := NewCopula([][]float64{{1, rho}, {rho, 1}}, copulaStream())
	if err != nil {
		t.Fatal(err)
	}
	var out [2]ColumnGen
	for j, name := range []string{"a", "b"} {
		s := streams(name)
		s.Copula = c.Slot(j)
		cl := col(name, "DOUBLE")
		cl.Rows = 20000
		if err := (correlated{}).Check(cl, nil); err != nil {
			t.Fatal(err)
		}
		if out[j], err = New(apitypes.GenCorrelated, cl, numberProfile(), nil, s); err != nil {
			t.Fatal(err)
		}
	}
	return out[0], out[1]
}

// ranks returns the midranks of xs.
func ranks(xs []float64) []float64 {
	idx := make([]int, len(xs))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return xs[idx[a]] < xs[idx[b]] })
	r := make([]float64, len(xs))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		for k := i; k <= j; k++ {
			r[idx[k]] = float64(i+j)/2 + 1
		}
		i = j + 1
	}
	return r
}

func spearman(a, b []float64) float64 {
	ra, rb := ranks(a), ranks(b)
	n := float64(len(ra))
	var ma, mb float64
	for i := range ra {
		ma += ra[i]
		mb += rb[i]
	}
	ma, mb = ma/n, mb/n
	var sab, saa, sbb float64
	for i := range ra {
		da, db := ra[i]-ma, rb[i]-mb
		sab += da * db
		saa += da * da
		sbb += db * db
	}
	return sab / math.Sqrt(saa*sbb)
}

// gridKS is the Kolmogorov-Smirnov distance of xs to the smooth grid
// distribution of q.
func gridKS(xs []float64, q []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	cdf := func(x float64) float64 {
		steps := len(q) - 1
		for i := range steps {
			if x < q[i+1] {
				return (float64(i) + (x-q[i])/(q[i+1]-q[i])) / float64(steps)
			}
		}
		return 1
	}
	n := float64(len(s))
	var d float64
	for i, x := range s {
		f := cdf(x)
		d = max(d, math.Abs(float64(i+1)/n-f), math.Abs(f-float64(i)/n))
	}
	return d
}

func TestCorrelatedDraws(t *testing.T) {
	ga, gb := corrPair(t, 0.8)
	var a, b, onlyA, onlyB []float64
	for i := range int64(20000) {
		row := RowCtx{Index: i}
		va, err := ga.Next(row)
		if err != nil {
			t.Fatal(err)
		}
		vb, err := gb.Next(row)
		if err != nil {
			t.Fatal(err)
		}
		if va != nil {
			onlyA = append(onlyA, va.(float64))
		}
		if vb != nil {
			onlyB = append(onlyB, vb.(float64))
		}
		if va != nil && vb != nil {
			a = append(a, va.(float64))
			b = append(b, vb.(float64))
		}
	}
	if rho := spearman(a, b); math.Abs(rho-0.8) > 0.03 {
		t.Errorf("Spearman = %v, want 0.8 +- 0.03", rho)
	}
	q := numberProfile().Quantiles
	for name, xs := range map[string][]float64{"a": onlyA, "b": onlyB} {
		if d := gridKS(xs, q); d > 0.02 {
			t.Errorf("%s: ks = %v, want <= 0.02", name, d)
		}
	}
}

func TestCopulaRowSkip(t *testing.T) {
	rho := [][]float64{{1, 0.6}, {0.6, 1}}
	all, _, _ := NewCopula(rho, copulaStream())
	want := map[int64][2]float64{}
	for row := range int64(7) {
		var u [2]float64
		for j := range 2 {
			u[j], _ = all.Slot(j).U(row)
		}
		want[row] = u
	}
	skip, _, _ := NewCopula(rho, copulaStream())
	for _, row := range []int64{5, 6} {
		for j := range 2 {
			u, err := skip.Slot(j).U(row)
			if err != nil || u != want[row][j] {
				t.Errorf("row %d member %d: %v (err %v), want %v", row, j, u, err, want[row][j])
			}
		}
	}
	if _, err := skip.Slot(0).U(4); err == nil {
		t.Error("going back did not fail")
	}
}

func TestCorrelatedWithoutGroup(t *testing.T) {
	_, err := New(apitypes.GenCorrelated, col("a", "DOUBLE"), numberProfile(), nil, streams("a"))
	if err == nil || !strings.Contains(err.Error(), "correlated column without a group") {
		t.Errorf("err = %v", err)
	}
}

func TestCorrelatedCheck(t *testing.T) {
	g := correlated{}
	for _, typ := range []string{"DATE", "TIMESTAMP", "TIMESTAMP WITH TIME ZONE", "VARCHAR"} {
		if err := g.Check(col("x", typ), nil); !errors.Is(err, ErrType) {
			t.Errorf("%s: err = %v, want ErrType", typ, err)
		}
	}
	for _, typ := range []string{"INTEGER", "DOUBLE", "DECIMAL(12,2)", "TIME"} {
		if err := g.Check(col("x", typ), nil); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	for _, a := range []string{"decimals", "integer"} {
		if err := g.Check(col("x", "TIME"), Args{a: 1.0}); err == nil {
			t.Errorf("TIME with %s: no error", a)
		}
	}
}

func TestCorrelatedGolden(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("golden values are pinned on amd64; math.Erfc and math.Sin may differ in the last bit elsewhere")
	}
	ga, gb := corrPair(t, 0.8)
	var got []string
	for i := range int64(4) {
		for _, g := range []ColumnGen{ga, gb} {
			v, err := g.Next(RowCtx{Index: i})
			if err != nil {
				t.Fatal(err)
			}
			if f, ok := v.(float64); ok {
				got = append(got, fmt.Sprintf("%b", f))
			} else {
				got = append(got, fmt.Sprint(v))
			}
		}
	}
	want := []string{
		"6917198340309018p-46", "6172394840681685p-46", "5208566488401320p-46", "8678250045813532p-47",
		"4719916719307197p-47", "5158914953501657p-47", "8319972829659276p-47", "5171015478765658p-48",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("values:\n got %q\nwant %q", got, want)
	}
}
