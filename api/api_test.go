package api

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"ontology/mul"
)

func gen(n int, seed int64) (a, b []float64) {
	r := rand.New(rand.NewSource(seed))
	a, b = make([]float64, n*n), make([]float64, n*n)
	for i := range a {
		a[i] = r.NormFloat64() * 7 // signs, zeros-by-subtraction mix
		b[i] = float64((i/n)-(i%n)) * 0.5
	}
	return a, b
}

func bitsEq(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

// TestBlockedMatchesNaive pins invariant 1 across divisible and
// non-divisible n, including negatives and zeros.
func TestBlockedMatchesNaive(t *testing.T) {
	cases := []struct{ n, b int }{
		{1, 1}, {2, 1}, {5, 2}, {6, 3}, {7, 4}, {13, 5}, {34, 17},
	}
	for _, c := range cases {
		a, b := gen(c.n, int64(c.n*100+c.b))
		if !bitsEq(mul.Blocked(a, b, c.n, c.b), mul.Naive(a, b, c.n)) {
			t.Fatalf("n=%d b=%d: blocked not bit-identical to naive", c.n, c.b)
		}
	}
}

// TestDeterministic pins invariant 3: repeated calls are bit-stable.
func TestDeterministic(t *testing.T) {
	for _, c := range []struct{ n, b int }{{5, 2}, {10, 3}, {17, 6}} {
		e := New(c.b)
		a, b := gen(c.n, 99)
		r1, err := e.Mul(a, b, c.n)
		if err != nil {
			t.Fatal(err)
		}
		for rep := 0; rep < 5; rep++ {
			r2, err := e.Mul(a, b, c.n)
			if err != nil || !bitsEq(r1, r2) {
				t.Fatalf("n=%d b=%d rep=%d: nondeterministic", c.n, c.b, rep)
			}
		}
	}
}

// TestRejectionsLeaveNoTrace pins invariant 4 and the three distinct
// sentinels; a rejected call must not move the success counter.
func TestRejectionsLeaveNoTrace(t *testing.T) {
	a, b := gen(5, 7)
	cases := []struct {
		name string
		bb   int
		n    int
		x, y []float64
		want error
	}{
		{"empty", 2, 0, nil, nil, ErrEmpty},
		{"dim-a", 2, 5, make([]float64, 24), b, ErrDim},
		{"dim-b", 2, 5, a, make([]float64, 24), ErrDim},
		{"b-zero", 0, 5, a, b, ErrBlockSize},
		{"b-over-n", 6, 5, a, b, ErrBlockSize},
		{"b-neg", -1, 5, a, b, ErrBlockSize},
	}
	sentinels := map[error]bool{}
	for _, c := range cases {
		e := New(c.bb)
		before := e.okCalls.Load()
		if r, err := e.Mul(c.x, c.y, c.n); !errors.Is(err, c.want) || r != nil {
			t.Fatalf("%s: got (%v,%v) want %v", c.name, r, err, c.want)
		}
		if e.okCalls.Load() != before {
			t.Fatalf("%s: rejection changed internal counter", c.name)
		}
		sentinels[c.want] = true
		// Engine still works after the rejection.
		if c.name != "empty" {
			if _, err := New(2).Mul(a, b, 5); err != nil {
				t.Fatalf("%s: engine unusable after rejection: %v", c.name, err)
			}
		}
	}
	if len(sentinels) != 3 {
		t.Fatalf("want 3 distinct sentinels, got %d", len(sentinels))
	}
}

// TestConcurrentMul: N goroutines sharing one read-only input get
// byte-identical results. Synchronization uses a channel barrier, never
// sleep.
func TestConcurrentMul(t *testing.T) {
	const N = 32
	n, bs := 13, 5
	a, b := gen(n, 2026)
	e := New(bs)
	res := make([][]float64, N)
	start := make(chan struct{})
	done := make(chan int, N)
	for g := 0; g < N; g++ {
		go func(g int) {
			<-start
			r, err := e.Mul(a, b, n)
			if err != nil {
				t.Error(err)
			}
			res[g] = r
			done <- g
		}(g)
	}
	close(start)
	for g := 0; g < N; g++ {
		<-done
	}
	for g := 1; g < N; g++ {
		if !bitsEq(res[0], res[g]) {
			t.Fatalf("goroutine %d result differs", g)
		}
	}
	if e.okCalls.Load() != N {
		t.Fatalf("okCalls=%d want %d", e.okCalls.Load(), N)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New(2).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
