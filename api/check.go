package api

import (
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
)

var errSelfCheck = errors.New("api: self-check failed")

func matProd(A, B []float64, n int) []float64 {
	C := make([]float64, n*n)
	for i := range n {
		for k := range n {
			aik := A[i*n+k]
			for j := range n {
				C[i*n+j] += aik * B[k*n+j]
			}
		}
	}
	return C
}

func maxResid(a, x, b []float64, n int) (m float64) {
	for i := range n {
		s := 0.0
		for j := range n {
			s += a[i*n+j] * x[j]
		}
		if d := math.Abs(s - b[i]); d > m {
			m = d
		}
	}
	return
}

func closeVec(p, q []float64, tol float64) bool {
	if len(p) != len(q) {
		return false
	}
	for i := range p {
		if d := p[i] - q[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func dimOf(a []float64) int { return int(math.Sqrt(float64(len(a)))) }

func encF64(x []float64) []byte {
	buf := make([]byte, 8*len(x))
	for i, v := range x {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

// synth returns A = L0*U0 (unit lower L0, non-zero diagonal U0).
func synth(n int, r *rand.Rand) (a, L0, U0 []float64) {
	L0, U0 = make([]float64, n*n), make([]float64, n*n)
	mults := []float64{-1.5, -0.5, 0, 0.5, 1.5}
	for i := range n {
		L0[i*n+i] = 1
		U0[i*n+i] = float64(1 + r.Intn(4))
		for j := i + 1; j < n; j++ {
			U0[i*n+j] = float64(r.Intn(5) - 2)
			L0[j*n+i] = mults[r.Intn(5)]
		}
	}
	return matProd(L0, U0, n), L0, U0
}

var fixedA = [][]float64{
	{2}, {1, -2, 3, 4}, {2, 1, 1, 4, 3, 3, 8, 7, 9}, {1, -1, 0, -2, 3, -1, 0, -1, 2},
}

// SelfCheck verifies the four invariants on scratch engines; receiver untouched.
func (e *Engine) SelfCheck() error {
	cs := []struct {
		n    int
		a, b []float64
	}{
		{1, []float64{3}, []float64{6}},
		{2, []float64{1, 0, 2, -3}, []float64{2, -7}},
		{3, []float64{2, 1, 1, 4, 3, 3, 8, 7, 9}, []float64{7, 19, 49}},
	}
	g := New()
	for _, c := range cs {
		if err := g.Factor(c.a, c.n); err != nil || triangular(g.L, g.U, c.n) != nil ||
			!closeVec(matProd(g.L, g.U, c.n), c.a, 1e-9) {
			return errSelfCheck
		}
		x, err := g.Solve(c.b, c.n) // solve against the cached factorization
		if err != nil || maxResid(c.a, x, c.b, c.n) > 1e-9 {
			return errSelfCheck
		}
	}
	if g.factorCount.Load() != int64(len(cs)) {
		return errSelfCheck
	}
	// Large m: counter must stay 1 across 10000 solves.
	a3 := cs[2].a
	g2 := New()
	if g2.Factor(a3, 3) != nil {
		return errSelfCheck
	}
	for i := range 10000 {
		rhs := []float64{float64(i%7) * 0.3, float64(i%5) * 0.2, float64(i%3) * 0.4}
		x, err := g2.Solve(rhs, 3)
		if err != nil || maxResid(a3, x, rhs, 3) > 1e-9 || g2.factorCount.Load() != 1 {
			return errSelfCheck
		}
	}
	return noTrace()
}

func triangular(L, U []float64, n int) error {
	for i := range n {
		for j := range n {
			if (i == j && L[i*n+j] != 1) ||
				(i < j && L[i*n+j] != 0) || (i > j && U[i*n+j] != 0) {
				return errSelfCheck
			}
		}
	}
	return nil
}

// noTrace confirms rejected calls never mutate cache or counter.
func noTrace() error {
	g := New()
	for _, d := range [][2]int{{0, 0}, {2, 3}, {2, 5}} { // (n,len(a)): empty,bad,bad
		if g.Factor(make([]float64, d[1]), d[0]) == nil {
			return errSelfCheck
		}
	}
	if g.L != nil || g.factorCount.Load() != 0 {
		return errSelfCheck
	}
	if _, err := g.Solve([]float64{1}, 1); !errors.Is(err, ErrNotFactored) {
		return errSelfCheck
	}
	if err := g.Factor([]float64{1, 2, 3, 2, 4, 6, 7, 8, 9}, 3); !errors.Is(err, ErrZeroPivot) ||
		g.L != nil || g.factorCount.Load() != 0 {
		return errSelfCheck
	}
	return nil
}
