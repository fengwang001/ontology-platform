package api

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/pivot"
)

// TestDetMatchesNaive pins invariant 1 against recursive first-row expansion.
func TestDetMatchesNaive(t *testing.T) {
	x := New()
	fixed := [][]float64{
		{7}, {-3}, {0},
		{0, 1, 1, 0},
		{0, 1, 1, 1, 0, 1, 1, 1, 0},
		{1, 2, 3, 2, 4, 6, 7, 8, 9}, // singular
	}
	rng := rand.New(rand.NewSource(11))
	for n := 1; n <= 6; n++ {
		m := make([]float64, n*n)
		for i := range m {
			m[i] = float64(rng.Intn(13) - 6)
		}
		fixed = append(fixed, m)
	}
	for _, a := range fixed {
		n := int(math.Sqrt(float64(len(a))))
		got, err := x.Det(a, n)
		if want := naiveDet(a, n); err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("n=%d a=%v: got (%v,%v), want naive %v", n, a, got, err, want)
		}
	}
}

// TestSwapSign pins invariant 2: sign is (-1)^swaps, positive with no swap.
func TestSwapSign(t *testing.T) {
	x := New()
	cases := []struct {
		m   []float64
		det float64
	}{
		{[]float64{1, 0, 0, 1}, 1},                 // identity, no swap
		{[]float64{4, 1, 2, 3}, 10},                // |4|>=|2|, no swap, positive
		{[]float64{0, 1, 0, 1, 0, 0, 0, 0, 1}, -1}, // one transposition
		{[]float64{0, 1, 0, 0, 0, 1, 1, 0, 0}, 1},  // 3-cycle, two swaps
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 2},  // NOTES worked example
	}
	for _, c := range cases {
		n := int(math.Sqrt(float64(len(c.m))))
		if got, err := x.Det(c.m, n); err != nil || math.Abs(got-c.det) > 1e-9 {
			t.Errorf("m=%v: got (%v,%v), want %v", c.m, got, err, c.det)
		}
	}
	// Random permutation matrices cover both inversion parities.
	prng := rand.New(rand.NewSource(23))
	for trial := 0; trial < 40; trial++ {
		n := 1 + prng.Intn(8)
		p := prng.Perm(n)
		m, parity := make([]float64, n*n), 1.0
		for i := 0; i < n; i++ {
			m[p[i]*n+i] = 1
			for j := i + 1; j < n; j++ {
				if p[i] > p[j] {
					parity = -parity
				}
			}
		}
		if got, err := x.Det(m, n); err != nil || got != parity {
			t.Errorf("perm=%v: got (%v,%v), want sign %v", p, got, err, parity)
		}
	}
}

// TestPick pins pivot selection: max magnitude, smallest index on a tie, false
// on an all-zero k..n-1 range (rows above k ignored).
func TestPick(t *testing.T) {
	cases := []struct {
		m      []float64
		k, row int
		ok     bool
	}{
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 0, 1, true},  // tie 1 at rows 1,2 -> 1
		{[]float64{1, 0, 1, 0, 1, 1, 0, 1, -1}, 1, 1, true}, // tie 1 at 1,2 -> 1
		{[]float64{1, 9, 0, 0}, 1, 1, false},                // above ignored; below zero
		{[]float64{-5, 0, -3, 1}, 0, 0, true},               // negative magnitude
		{[]float64{1, 0, -9, 1}, 0, 1, true},
	}
	for _, c := range cases {
		n := int(math.Sqrt(float64(len(c.m))))
		if row, ok := pivot.Pick(c.m, n, c.k); row != c.row || ok != c.ok {
			t.Errorf("Pick k=%d m=%v: got (%d,%v), want (%d,%v)", c.k, c.m, row, ok, c.row, c.ok)
		}
	}
}

// TestInputNotModified pins invariant 3 on accepted and rejected inputs.
func TestInputNotModified(t *testing.T) {
	x := New()
	cases := []struct {
		m []float64
		n int
	}{
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 3},
		{[]float64{1, 2, 3, 4}, 2},
		{nil, 0},
		{[]float64{1, 2, 3}, 2},
		{[]float64{1, math.NaN(), 0, 1}, 2},
	}
	for _, c := range cases {
		before := append([]float64(nil), c.m...)
		x.Det(c.m, c.n)
		for i := range c.m {
			if math.Float64bits(c.m[i]) != math.Float64bits(before[i]) {
				t.Errorf("Det modified input n=%d: before %v after %v", c.n, before, c.m)
			}
		}
	}
}

// TestSelfCheck asserts the externally callable self-check passes.
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentDetByteIdentical pins invariant 6; WaitGroup is the only sync.
func TestConcurrentDetByteIdentical(t *testing.T) {
	x, rng := New(), rand.New(rand.NewSource(31))
	a := make([]float64, 64)
	for i := range a {
		a[i] = float64(rng.Intn(11) - 5)
	}
	const N = 64
	res := make([]uint64, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { d, _ := x.Det(a, 8); res[g] = math.Float64bits(d); wg.Done() }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if res[g] != res[0] {
			t.Fatalf("goroutine %d bits %#x, want %#x", g, res[g], res[0])
		}
	}
}
