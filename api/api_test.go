package api_test

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/api"
	"ontology/norm"
	"ontology/normest"
)

// TestWorkedExample pins the section-three result: κ∞ estimate is exactly 10.
func TestWorkedExample(t *testing.T) {
	e, err := api.New(2)
	if err != nil {
		t.Fatal(err)
	}
	k, err := e.Cond([]float64{1, 3, 0, 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(k-10) > 1e-9 {
		t.Errorf("κ = %v, want 10", k)
	}
}

// TestCondErrors pins invariant 4's four distinct, decidable sentinel
// errors; each rejected call leaves the estimator usable afterwards.
func TestCondErrors(t *testing.T) {
	good := []float64{1, 3, 0, 2}
	e, _ := api.New(2)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"bad-k", func() error { _, err := api.New(0); return err }, normest.ErrInvalidK},
		{"empty", func() error { _, err := e.Cond(good, 0); return err }, norm.ErrEmpty},
		{"dimension", func() error { _, err := e.Cond(good[:3], 2); return err }, norm.ErrDimension},
		{"singular", func() error { _, err := e.Cond([]float64{1, 1, 1, 1}, 2); return err }, norm.ErrSingular},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		err := c.call()
		if !errors.Is(err, c.want) || err != c.want {
			t.Errorf("%s: err = %v, want sentinel %v", c.name, err, c.want)
		}
		if seen[err] {
			t.Errorf("%s: error not distinct", c.name)
		}
		seen[err] = true
		if _, err := e.Cond(good, 2); err != nil {
			t.Errorf("%s: estimator unusable after rejection: %v", c.name, err)
		}
	}
}

// TestSelfCheck verifies the built-in lower-bound checks across matrices
// containing negative and zero elements.
func TestSelfCheck(t *testing.T) {
	for _, k := range []int{1, 2, 5} {
		e, err := api.New(k)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.SelfCheck(); err != nil {
			t.Errorf("k=%d: %v", k, err)
		}
	}
}

// TestCondConcurrent pins the race requirement: N goroutines share one
// estimator and one read-only input and get byte-identical κ values.
func TestCondConcurrent(t *testing.T) {
	a := []float64{1, 3, 0, 2}
	e, _ := api.New(2)
	const N = 64
	var wg sync.WaitGroup
	res := make([]uint64, N)
	errs := make([]error, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k, err := e.Cond(a, 2)
			res[g], errs[g] = math.Float64bits(k), err
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if errs[g] != nil {
			t.Fatalf("goroutine %d: %v", g, errs[g])
		}
		if res[g] != res[0] {
			t.Errorf("goroutine %d: %016x != %016x", g, res[g], res[0])
		}
	}
}

// TestInputReadOnly verifies Cond never mutates its input slice.
func TestInputReadOnly(t *testing.T) {
	a := []float64{1, 3, 0, 2}
	snap := append([]float64(nil), a...)
	e, _ := api.New(3)
	if _, err := e.Cond(a, 2); err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != snap[i] {
			t.Fatalf("input mutated at %d: %v", i, a)
		}
	}
}
