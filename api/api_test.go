package api_test

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/elim"
	"ontology/pivot"
)

func dominant(r *rand.Rand, n int) []float64 {
	a := make([]float64, n*n)
	for i := 0; i < n; i++ {
		sum := 1.0
		for j := 0; j < n; j++ {
			if j != i {
				v := r.Float64()*2 - 1
				a[i*n+j], sum = v, sum+math.Abs(v)
			}
		}
		a[i*n+i] = sum + r.Float64()
	}
	return a
}
func TestSolveResidual(t *testing.T) {
	s := api.New()
	type sys struct {
		a, b []float64
		n    int
	}
	systems := []sys{
		{[]float64{2}, []float64{6}, 1},
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}, 3},
	}
	r := rand.New(rand.NewSource(7))
	for _, sz := range []int{1, 2, 4, 16, 64} {
		b := make([]float64, sz)
		for j := range b {
			b[j] = r.Float64()*4 - 2
		}
		systems = append(systems, sys{dominant(r, sz), b, sz})
	}
	for _, c := range systems {
		x, err := s.Solve(c.a, c.b, c.n)
		if err != nil {
			t.Fatalf("solve: %v", err)
		}
		for i := 0; i < c.n; i++ {
			d := -c.b[i]
			for j := 0; j < c.n; j++ {
				d += c.a[i*c.n+j] * x[j]
			}
			if math.Abs(d) > 1e-9 {
				t.Fatalf("row %d residual %v", i, d)
			}
		}
	}
}
func TestInputNotModified(t *testing.T) {
	s := api.New()
	for _, c := range []struct {
		a, b []float64
		n    int
	}{
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}, 3},
		{[]float64{-2, 1, 1, -2}, []float64{0, -3}, 2},
	} {
		a0, b0 := append([]float64(nil), c.a...), append([]float64(nil), c.b...)
		if _, err := s.Solve(c.a, c.b, c.n); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(c.a, a0) || !slices.Equal(c.b, b0) {
			t.Fatal("Solve modified its input")
		}
	}
}
func TestPivotTieSmallest(t *testing.T) {
	for _, c := range []struct {
		a       []float64
		n, k, w int
	}{
		{[]float64{1, 2, 1, 3}, 2, 0, 0},
		{[]float64{0.2, 0, 0, 0.5, 0, 0, 0.5, 0, 0}, 3, 0, 1},
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 3, 0, 1},
	} {
		r1, ok1 := pivot.Pick(c.a, c.n, c.k)
		r2, ok2 := pivot.Pick(c.a, c.n, c.k)
		if r1 != c.w || !ok1 || r2 != r1 || ok2 != ok1 {
			t.Fatalf("Pick=(%d,%v),(%d,%v) want stable %d", r1, ok1, r2, ok2, c.w)
		}
	}
}
func TestRejectedLeavesNoTrace(t *testing.T) {
	s := api.New()
	seen := map[error]bool{}
	for _, c := range []struct {
		name string
		a, b []float64
		n    int
		want error
	}{
		{"empty", nil, nil, 0, elim.ErrEmpty},
		{"bad-len-a", []float64{1, 2, 3}, []float64{1, 2}, 2, elim.ErrDimension},
		{"bad-len-b", []float64{1, 0, 0, 1}, []float64{1}, 2, elim.ErrDimension},
		{"singular", []float64{1, 0, 0, 0}, []float64{1, 2}, 2, elim.ErrSingular},
	} {
		a0, b0 := append([]float64(nil), c.a...), append([]float64(nil), c.b...)
		if _, err := s.Solve(c.a, c.b, c.n); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if !slices.Equal(c.a, a0) || !slices.Equal(c.b, b0) {
			t.Fatalf("%s: rejected call mutated input", c.name)
		}
		seen[c.want] = true
	}
	if len(seen) != 3 {
		t.Fatalf("rejection errors not distinct: %d unique", len(seen))
	}
	x, err := s.Solve([]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}, 3)
	if err != nil || !slices.Equal(x, []float64{1, 2, 3}) {
		t.Fatalf("solver unusable after rejects: %v %v", x, err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestConcurrentSolveByteIdentical(t *testing.T) {
	s := api.New()
	a, b := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}
	const N = 64
	var wg sync.WaitGroup
	res := make([][]float64, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) { defer wg.Done(); res[g], _ = s.Solve(a, b, 3) }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if !slices.Equal(res[0], res[g]) {
			t.Fatalf("goroutine %d got %v want %v", g, res[g], res[0])
		}
	}
}
