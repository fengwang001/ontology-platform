package elim

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// TestRejectionLeavesNoTrace pins invariant 4: the three rejection classes
// are distinguishable sentinels arriving in randomized order, the internal ops
// counter does not move, and valid matrices still serve afterwards.
func TestRejectionLeavesNoTrace(t *testing.T) {
	a := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	if d, err := Det(a, 3); err != nil || d != 2 {
		t.Fatalf("baseline: got (%v,%v), want (2,nil)", d, err)
	}
	before := ops.Load()
	cases := []struct {
		name string
		a    []float64
		n    int
		want error
	}{
		{"empty n=0", nil, 0, ErrEmpty},
		{"empty n<0", []float64{1}, -1, ErrEmpty},
		{"dim short", []float64{1, 2, 3}, 2, ErrDim},
		{"dim long", make([]float64, 10), 2, ErrDim},
		{"nan", []float64{1, math.NaN(), 0, 1}, 2, ErrNaNOrInf},
		{"+inf", []float64{1, math.Inf(1), 0, 1}, 2, ErrNaNOrInf},
		{"-inf", []float64{1, 2, math.Inf(-1), 1}, 2, ErrNaNOrInf},
	}
	rng := rand.New(rand.NewSource(7))
	rng.Shuffle(len(cases), func(i, j int) { cases[i], cases[j] = cases[j], cases[i] })
	for _, c := range cases {
		if _, err := Det(c.a, c.n); !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", c.name, c.want, err)
		}
	}
	if ErrEmpty == ErrDim || ErrDim == ErrNaNOrInf || ErrEmpty == ErrNaNOrInf {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	if ops.Load() != before {
		t.Fatalf("rejection changed ops: before %d after %d", before, ops.Load())
	}
	if d, err := Det(a, 3); err != nil || d != 2 {
		t.Fatalf("unusable after rejection: got (%v,%v), want (2,nil)", d, err)
	}
}

// TestTriangularNoOps pins the complexity claim: an already upper triangular
// matrix at n=100/1000/10000 adds exactly 0 multiply-subtract ops.
func TestTriangularNoOps(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		before := ops.Load()
		m := make([]float64, n*n)
		want := 1.0
		for i := 0; i < n; i++ {
			m[i*n+i] = float64(1 + i%7)
			want *= m[i*n+i]
			for j := i + 1; j < n; j++ {
				m[i*n+j] = float64((i*3 + j) % 5)
			}
		}
		d, err := Det(m, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := ops.Load() - before; got != 0 {
			t.Fatalf("n=%d: %d ops, want 0", n, got)
		}
		if d != want {
			t.Fatalf("n=%d: det=%v want %v", n, d, want)
		}
	}
}

// TestOpsCounterCounts proves the counter is live by measuring its increment.
func TestOpsCounterCounts(t *testing.T) {
	cases := []struct {
		m   []float64
		n   int
		add int64
		det float64
	}{
		{[]float64{1, 2, 3, 4}, 2, 1, -2},
		{[]float64{2, 0, 0, 3}, 2, 0, 6},
		{[]float64{2, 1, 0, 1, 3, 2, 1, 0, 3}, 3, 3, 17},
	}
	for _, c := range cases {
		before := ops.Load()
		d, err := Det(c.m, c.n)
		add := ops.Load() - before
		if err != nil || math.Abs(d-c.det) > 1e-9 || add != c.add {
			t.Errorf("m=%v: det=%v add=%d err=%v, want det=%v add=%d", c.m, d, add, err, c.det, c.add)
		}
	}
}
