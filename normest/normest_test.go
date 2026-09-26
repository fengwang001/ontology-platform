package normest

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/norm"
)

func bidiag(n int) []float64 {
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		a[i*n+i] = 2
		if i+1 < n {
			a[i*n+i+1] = 1
		}
	}
	return a
}

// Invariants 2 and 3: the number of solves is exactly k and the number of
// factorizations is exactly 1, independent of n (100/1000/10000).
func TestSolveCounterConstantInN(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		a := bidiag(n)
		s0, f0 := solves.Load(), factors.Load()
		if _, err := EstimateInvInf(a, n, 3); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := solves.Load() - s0; got != 3 {
			t.Fatalf("n=%d: solve count delta=%d, want 3", n, got)
		}
		if got := factors.Load() - f0; got != 1 {
			t.Fatalf("n=%d: factor count delta=%d, want 1", n, got)
		}
	}
}

// Worked example round by round (spec section 3).
func TestEstimateTraceN2(t *testing.T) {
	est, rs, err := estimate([]float64{1, 3, 0, 2}, 2, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if est != 2.5 || len(rs) != 2 {
		t.Fatalf("est=%v rounds=%d", est, len(rs))
	}
	want := []round{
		{[]float64{1, 1}, []float64{-0.5, 0.5}, []float64{-1, 1}, 0.5},
		{[]float64{-1, 1}, []float64{-2.5, 0.5}, []float64{-1, 1}, 2.5},
	}
	for i := range want {
		for j, v := range want[i].b {
			if rs[i].b[j] != v || rs[i].y[j] != want[i].y[j] ||
				rs[i].sign[j] != want[i].sign[j] {
				t.Fatalf("round %d trace mismatch: %+v want %+v", i, rs[i], want[i])
			}
		}
		if rs[i].yInf != want[i].yInf {
			t.Fatalf("round %d yInf=%v want %v", i, rs[i].yInf, want[i].yInf)
		}
	}
}

func TestEstimateTable(t *testing.T) {
	cases := []struct {
		name string
		a    []float64
		n, k int
		want float64
	}{
		{"n=1", []float64{4}, 1, 3, 0.25},
		{"diag", []float64{2, 0, 0, 8}, 2, 2, 0.5},
		{"negatives", []float64{-2, 0, 0, -4}, 2, 1, 0.5},
		{"worked", []float64{1, 3, 0, 2}, 2, 2, 2.5},
	}
	for _, c := range cases {
		got, err := EstimateInvInf(c.a, c.n, c.k)
		if err != nil || got != c.want {
			t.Fatalf("%s: got %v, %v want %v", c.name, got, err, c.want)
		}
	}
}

// Invariant 4: every rejected call (four distinct classes) is a total
// failure and leaves both counters untouched. Arrival order is shuffled.
func TestRejectionLeavesNoTrace(t *testing.T) {
	calls := []struct {
		name string
		a    []float64
		n, k int
		want error
	}{
		{"empty", nil, 0, 1, norm.ErrEmpty},
		{"dim", []float64{1, 0, 0, 1, 0, 0, 0, 0}, 3, 1, norm.ErrDim},
		{"singular", []float64{1, 2, 2, 4}, 2, 1, norm.ErrSingular},
		{"badk", bidiag(4), 4, 0, ErrBadK},
	}
	for seed := int64(0); seed < 5; seed++ {
		for _, idx := range rand.New(rand.NewSource(seed)).Perm(len(calls)) {
			c := calls[idx]
			s0, f0 := solves.Load(), factors.Load()
			if _, err := EstimateInvInf(c.a, c.n, c.k); !errors.Is(err, c.want) {
				t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
			}
			if solves.Load() != s0 || factors.Load() != f0 {
				t.Fatalf("%s: counters changed after rejection", c.name)
			}
		}
	}
}

// norm primitives through their exported API: negatives/zeros included.
func TestNormPrimitives(t *testing.T) {
	a := []float64{1, -3, 0, 2}
	if got := norm.InfNorm(a, 2); got != 4 {
		t.Fatalf("InfNorm=%v want 4", got)
	}
	L, U, err := norm.Factor([]float64{1, 3, 0, 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	y := norm.Solve(L, U, 2, []float64{1, 1})
	if y[0] != -0.5 || y[1] != 0.5 {
		t.Fatalf("solve=%v want [-0.5 0.5]", y)
	}
	if _, _, err := norm.Factor([]float64{1, 2, 2, 4}, 2); !errors.Is(err, norm.ErrSingular) {
		t.Fatalf("singular: %v", err)
	}
}
