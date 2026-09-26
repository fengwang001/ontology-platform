package lsq

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/gram"
)

// naiveGram independently fills EVERY entry (both triangles) with its
// own dot product; gram.Gram must match it bit for bit, proving both
// exact symmetry and that the full symmetric matrix is represented.
func naiveGram(a []float64, m, n int) []float64 {
	g := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var s float64
			for k := 0; k < m; k++ {
				s += a[k*n+i] * a[k*n+j]
			}
			g[i*n+j] = s
		}
	}
	return g
}

func TestGramSymmetric(t *testing.T) {
	cases := []struct{ m, n int }{
		{3, 2}, {10, 1}, {50, 4}, {200, 7}, {1000, 3},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(int64(tc.m*31 + tc.n)))
		a := make([]float64, tc.m*tc.n)
		for i := range a {
			a[i] = rng.Float64()*4 - 2 // negatives, zeros possible
		}
		g := gram.Gram(a, tc.m, tc.n)
		ref := naiveGram(a, tc.m, tc.n)
		for i := 0; i < tc.n; i++ {
			for j := 0; j < tc.n; j++ {
				if g[i*tc.n+j] != ref[i*tc.n+j] {
					t.Fatalf("m=%d n=%d Gram[%d][%d]=%v want %v", tc.m, tc.n, i, j, g[i*tc.n+j], ref[i*tc.n+j])
				}
			}
		}
		// The worked example solved on the full symmetric matrix.
		if tc.m == 3 && tc.n == 2 {
			continue
		}
		c := make([]float64, tc.n)
		for i := range c {
			c[i] = rng.Float64()
		}
		x, err := SolveG(g, c, tc.n)
		if err != nil {
			t.Fatalf("m=%d n=%d: unexpected error %v", tc.m, tc.n, err)
		}
		for i := 0; i < tc.n; i++ {
			var got float64
			for j := 0; j < tc.n; j++ {
				got += g[i*tc.n+j] * x[j]
			}
			if math.Abs(got-c[i]) > 1e-9 {
				t.Fatalf("m=%d n=%d row %d: G·x=%v want %v", tc.m, tc.n, i, got, c[i])
			}
		}
	}
}

func TestSolveG(t *testing.T) {
	// Worked example: G=[[3,6],[6,14]], c=[10,23] → x=[1/3,3/2].
	x, err := SolveG([]float64{3, 6, 6, 14}, []float64{10, 23}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(x[0]-1.0/3) > 1e-12 || math.Abs(x[1]-1.5) > 1e-12 {
		t.Fatalf("x=%v want [1/3 3/2]", x)
	}
	// Inputs must not be modified.
	g := []float64{3, 6, 6, 14}
	c := []float64{10, 23}
	SolveG(g, c, 2)
	if g[0] != 3 || g[2] != 6 || c[0] != 10 {
		t.Fatalf("SolveG mutated its inputs: g=%v c=%v", g, c)
	}
	// Rank-deficient: duplicate columns make G singular.
	if _, err := SolveG([]float64{2, 2, 2, 2}, []float64{1, 1}, 2); !errors.Is(err, ErrSingular) {
		t.Fatalf("err=%v want ErrSingular", err)
	}
}
