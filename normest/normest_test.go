package normest

import (
	"math"
	"math/rand"
	"testing"

	"ontology/norm"
)

// refInv forms the explicit inverse of a (test reference only; never used
// by the estimator) via Gauss-Jordan with partial pivoting.
func refInv(a []float64, n int) []float64 {
	w := 2 * n
	m := make([]float64, n*w)
	for i := 0; i < n; i++ {
		copy(m[i*w:i*w+n], a[i*n:(i+1)*n])
		m[i*w+n+i] = 1
	}
	for c := 0; c < n; c++ {
		p, pv := c, math.Abs(m[c*w+c])
		for r := c + 1; r < n; r++ {
			if v := math.Abs(m[r*w+c]); v > pv {
				p, pv = r, v
			}
		}
		for j := 0; j < w; j++ {
			t1, t2 := m[c*w+j], m[p*w+j]
			m[c*w+j], m[p*w+j] = t2, t1
		}
		d := m[c*w+c]
		for j := 0; j < w; j++ {
			m[c*w+j] /= d
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := m[r*w+c]
			for j := 0; j < w; j++ {
				m[r*w+j] -= f * m[c*w+j]
			}
		}
	}
	inv := make([]float64, n*n)
	for i := 0; i < n; i++ {
		copy(inv[i*n:(i+1)*n], m[i*w+n:(i+1)*w])
	}
	return inv
}

// newMMatrix returns a random strictly diagonally dominant M-matrix:
// positive diagonal, non-positive off-diagonal entries that are randomly
// zero or negative. Its inverse is entrywise non-negative, so the
// all-ones solve already reaches the inverse's max row sum.
func newMMatrix(r *rand.Rand, n int) []float64 {
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		var off float64
		for j := 0; j < n; j++ {
			if i != j && r.Intn(2) == 0 {
				v := r.Float64() / float64(n)
				a[i*n+j] = -v
				off += v
			}
		}
		a[i*n+i] = off + 0.5 + r.Float64() // strict dominance, positive pivot
	}
	return a
}

// TestEstimateRandom pins invariant 1 in shuffled arrival order on random
// M-matrices: est ≤ true κ and est ≥ true κ/n, for every k from 1 to 3.
func TestEstimateRandom(t *testing.T) {
	var sizes []int
	for _, n := range []int{1, 2, 3, 7, 17} {
		for range 3 {
			sizes = append(sizes, n)
		}
	}
	rand.New(rand.NewSource(42)).Shuffle(len(sizes),
		func(i, j int) { sizes[i], sizes[j] = sizes[j], sizes[i] })
	for idx, n := range sizes {
		a := newMMatrix(rand.New(rand.NewSource(int64(n*10+idx))), n)
		na := norm.InfNorm(a, n)
		trueInv := norm.InfNorm(refInv(a, n), n)
		trueK := na * trueInv
		tol := 1e-9 * math.Max(1, trueK)
		for _, k := range []int{1, 2, 3} {
			inv, err := EstimateInvInf(a, n, k)
			if err != nil {
				t.Fatalf("n=%d k=%d: %v", n, k, err)
			}
			estK := na * inv
			if estK > trueK+tol {
				t.Errorf("n=%d k=%d: est %g exceeds true κ %g", n, k, estK, trueK)
			}
			if estK < trueK/float64(n)-tol {
				t.Errorf("n=%d k=%d: est %g below true κ/n = %g", n, k, estK, trueK/float64(n))
			}
		}
	}
}

// TestCounterConstant pins invariants 2 and 3: exactly k solves regardless
// of n, with a single reused factorization.
func TestCounterConstant(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n)
		for i := 0; i < n; i++ {
			a[i*n+i] = float64(1 + i%7)
		}
		before := calls()
		if _, err := EstimateInvInf(a, n, 3); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := calls() - before; got != 3 {
			t.Errorf("n=%d: solve count delta = %d, want 3", n, got)
		}
	}
}

// TestErrorsStateUnchanged pins invariant 4: rejected calls increment no
// counter and the estimator stays usable.
func TestErrorsStateUnchanged(t *testing.T) {
	good := []float64{1, 3, 0, 2}
	cases := []struct {
		a    []float64
		n, k int
		want error
	}{
		{good, 2, 0, ErrInvalidK},
		{good, 0, 1, norm.ErrEmpty},
		{good[:3], 2, 1, norm.ErrDimension},
		{[]float64{1, 1, 1, 1}, 2, 1, norm.ErrSingular},
	}
	for _, c := range cases {
		before := calls()
		if _, err := EstimateInvInf(c.a, c.n, c.k); err != c.want {
			t.Errorf("%v: err = %v, want %v", c.want, err, c.want)
		}
		if calls() != before {
			t.Errorf("%v: counter changed on rejection", c.want)
		}
	}
	if _, err := EstimateInvInf(good, 2, 2); err != nil {
		t.Fatalf("usable after rejections: %v", err)
	}
}
