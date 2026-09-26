package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func TestCondTable(t *testing.T) {
	cases := []struct {
		name string
		a    []float64
		n, k int
		want float64 // estimator is exact on these structured matrices
	}{
		{"scalar", []float64{4}, 1, 3, 1},
		{"diag", []float64{2, 0, 0, 8}, 2, 2, 4},
		{"neg-diag", []float64{-2, 0, 0, -4}, 2, 1, 2},
		{"worked", []float64{1, 3, 0, 2}, 2, 2, 10},
		{"lower", []float64{2, 0, 1, 4}, 2, 2, 2.5},
	}
	for _, c := range cases {
		e, _ := api.New(c.k)
		got, err := e.Cond(c.a, c.n)
		if err != nil || got != c.want {
			t.Fatalf("%s: got %v, %v want %v", c.name, got, err, c.want)
		}
	}
	if _, err := api.New(0); !errors.Is(err, api.ErrBadK) {
		t.Fatalf("New(0): %v", err)
	}
}

// Invariant 1: est <= true and est >= true/n on built-in matrices whose
// true κ∞ is hand-computed.
func TestCondBounds(t *testing.T) {
	cases := []struct {
		a     []float64
		n, k  int
		trueK float64
	}{
		{[]float64{1, 3, 0, 2}, 2, 2, 10},
		{[]float64{2, 0, 0, 8}, 2, 3, 4},
		{[]float64{4}, 1, 1, 1},
	}
	for _, c := range cases {
		e, _ := api.New(c.k)
		got, err := e.Cond(c.a, c.n)
		if err != nil {
			t.Fatal(err)
		}
		if got > c.trueK*(1+1e-12) || got < c.trueK/float64(c.n) {
			t.Fatalf("est=%v outside [true/n=%v, true=%v]",
				got, c.trueK/float64(c.n), c.trueK)
		}
	}
}

// Invariant 4: four distinct, judgeable sentinels; arrival order shuffled;
// estimator still usable afterwards.
func TestSentinelErrors(t *testing.T) {
	e, _ := api.New(2)
	calls := []struct {
		f    func() error
		want error
	}{
		{func() error { _, err := e.Cond(nil, 0); return err }, api.ErrEmpty},
		{func() error { _, err := e.Cond([]float64{1, 0, 0, 1, 0, 0, 0, 0}, 3); return err }, api.ErrDim},
		{func() error { _, err := e.Cond([]float64{1, 2, 2, 4}, 2); return err }, api.ErrSingular},
		{func() error { _, err := api.New(0); return err }, api.ErrBadK},
	}
	all := []error{api.ErrEmpty, api.ErrDim, api.ErrSingular, api.ErrBadK}
	for seed := int64(0); seed < 5; seed++ {
		for _, idx := range rand.New(rand.NewSource(seed)).Perm(len(calls)) {
			if err := calls[idx].f(); !errors.Is(err, calls[idx].want) {
				t.Fatalf("case %d: err=%v want %v", idx, err, calls[idx].want)
			}
		}
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) {
				t.Fatal("sentinel errors not distinct")
			}
		}
	}
	if _, err := e.Cond([]float64{1, 3, 0, 2}, 2); err != nil {
		t.Fatalf("estimator unusable after rejections: %v", err)
	}
}

func TestCondDoesNotMutateInput(t *testing.T) {
	a := []float64{1, 3, 0, 2}
	snap := append([]float64(nil), a...)
	e, _ := api.New(3)
	if _, err := e.Cond(a, 2); err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != snap[i] {
			t.Fatal("Cond mutated its input slice")
		}
	}
}

func TestConcurrentCond(t *testing.T) {
	const n, N = 128, 64
	a := make([]float64, n*n) // invertible upper-bidiagonal
	for i := 0; i < n; i++ {
		a[i*n+i] = 4
		if i+1 < n {
			a[i*n+i+1] = 1
		}
	}
	e, _ := api.New(3)
	bits := make([]uint64, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			v, err := e.Cond(a, n) // same read-only input
			if err != nil {
				t.Errorf("goroutine %d: %v", g, err)
				return
			}
			bits[g] = math.Float64bits(v)
		}(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if bits[g] != bits[0] {
			t.Fatalf("goroutine %d: bits %016x != %016x", g, bits[g], bits[0])
		}
	}
}

func TestSelfCheck(t *testing.T) {
	e, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
