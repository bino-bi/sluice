// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"fmt"
	"math"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// minPivot is the smallest Cholesky pivot that counts as positive.
const minPivot = 1e-9

// Copula is the Gaussian copula of one correlated group. It draws the
// uniforms of all members row by row.
type Copula struct {
	l   [][]float64
	r   *rng.Stream
	row int64
	z0  []float64
	u   []float64
}

// CopulaSlot is the view of one member on its group.
type CopulaSlot struct {
	c *Copula
	j int
}

// NewCopula checks rho, converts it to a Gaussian correlation, shrinks it
// until Cholesky works, and returns the group and the shrinkage. rho is
// the Spearman matrix of the members.
func NewCopula(rho [][]float64, r *rng.Stream) (*Copula, float64, error) {
	m := len(rho)
	if m == 0 {
		return nil, 0, errors.New("correlation matrix is empty")
	}
	for i := range rho {
		if len(rho[i]) != m {
			return nil, 0, errors.New("correlation matrix is not square")
		}
		if rho[i][i] != 1 {
			return nil, 0, errors.New("correlation matrix has a diagonal entry other than 1")
		}
		for j := range i {
			x := rho[i][j]
			if math.IsNaN(x) || x < -1 || x > 1 {
				return nil, 0, errors.New("correlation matrix has an entry outside [-1, 1]")
			}
			if rho[j][i] != x {
				return nil, 0, errors.New("correlation matrix is not symmetric")
			}
		}
	}
	// Spearman to Pearson of the underlying normals, exact for a
	// Gaussian copula.
	g := make([][]float64, m)
	for i := range g {
		g[i] = make([]float64, m)
		for j := range g[i] {
			g[i][j] = float64(2 * math.Sin(float64(math.Pi*rho[i][j])/6))
		}
	}
	// Pairs are estimated from different rows, so g may not be positive
	// definite. Shrink toward the identity; at 1 it is the identity.
	a := make([][]float64, m)
	for i := range a {
		a[i] = make([]float64, m)
	}
	for step := 0; step <= 100; step++ {
		lambda := float64(step) / 100
		for i := range a {
			for j := range a[i] {
				if i == j {
					a[i][j] = 1
				} else {
					a[i][j] = float64((1 - lambda) * g[i][j])
				}
			}
		}
		if l, ok := cholesky(a); ok {
			return &Copula{l: l, r: r, row: -1, z0: make([]float64, m), u: make([]float64, m)}, lambda, nil
		}
	}
	return nil, 0, errors.New("correlation matrix has no Cholesky factor")
}

// cholesky returns the lower factor L of a with a = L L^T. ok is false
// when a pivot is not above minPivot.
func cholesky(a [][]float64) ([][]float64, bool) {
	m := len(a)
	l := make([][]float64, m)
	for i := range l {
		l[i] = make([]float64, m)
		for j := 0; j <= i; j++ {
			s := a[i][j]
			for k := range j {
				s -= float64(l[i][k] * l[j][k])
			}
			if i == j {
				if !(s > minPivot) {
					return nil, false
				}
				l[i][i] = math.Sqrt(s)
			} else {
				l[i][j] = s / l[j][j]
			}
		}
	}
	return l, true
}

// normCDF is the standard normal distribution function.
func normCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// normU maps z to a uniform below 1, as gridValue needs.
func normU(z float64) float64 {
	return min(normCDF(z), 1-0x1p-53)
}

// Slot returns the view of member j.
func (c *Copula) Slot(j int) *CopulaSlot { return &CopulaSlot{c: c, j: j} }

// U returns the uniform of member j in row. Rows must not go back. Rows
// that no member asks for are still drawn, so row i gets the same
// uniforms whatever the null pattern.
func (s *CopulaSlot) U(row int64) (float64, error) {
	c := s.c
	if row < c.row {
		return 0, errors.New("copula row went back")
	}
	for c.row < row {
		c.row++
		for j := range c.z0 {
			c.z0[j] = c.r.Norm()
		}
		for i, li := range c.l {
			var z float64
			for j := 0; j <= i; j++ {
				z += float64(li[j] * c.z0[j])
			}
			c.u[i] = normU(z)
		}
	}
	return c.u[s.j], nil
}

type correlated struct{}

func (correlated) Check(col Column, args Args) error {
	k := col.Kind()
	if err := fits(col, apitypes.GenCorrelated, isNumber(k) || k == profile.KindTime); err != nil {
		return err
	}
	if !isNumber(k) {
		for _, a := range []string{"decimals", "integer"} {
			if _, ok := args[a]; ok {
				return fmt.Errorf("%s is only for number columns", a)
			}
		}
	}
	return nil
}

func (correlated) Needs(col Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartQuantiles, Unit: roundingOf(col, args).unit()}
}

func (correlated) New(col Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	if s.Copula == nil {
		return nil, errors.New("correlated column without a group")
	}
	if len(p.Quantiles) < 2 {
		return nil, errNoGrid
	}
	return &corrGen{numGen: newNumGen(col, apitypes.GenCorrelated, p, args, s), q: p.Quantiles, slot: s.Copula, smooth: args.boolOr("smooth", true)}, nil
}

// corrGen is distribution with the uniform taken from the copula.
type corrGen struct {
	*numGen
	q      []float64
	slot   *CopulaSlot
	smooth bool
}

func (g *corrGen) Next(row RowCtx) (any, error) {
	u, err := g.slot.U(row.Index)
	if err != nil {
		return nil, err
	}
	return fromAxis(g.st, g.round.fit(gridValue(g.q, u, g.smooth), g.lo, g.hi)), nil
}
