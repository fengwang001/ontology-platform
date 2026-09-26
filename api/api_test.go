package api

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/gram"
)

func gen(rng *rand.Rand, k int) []float64 {
	v := make([]float64, k)
	for i := range v {
		v[i] = rng.Float64()*20 - 10 // negative, zero or positive entries
	}
	return v
}

// atRes returns Aᵀ(Ax−b), the zero vector exactly at the least-squares optimum.
func atRes(a, x, b []float64, m, n int) []float64 {
	r := make([]float64, m)
	for k := 0; k < m; k++ {
		s := -b[k]
		for j := 0; j < n; j++ {
			s += a[k*n+j] * x[j]
		}
		r[k] = s
	}
	return gram.AtB(a, r, m, n)
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Invariant 1: x minimizes ‖Ax−b‖² ⇔ every component of Aᵀ(Ax−b) is 0.
func TestOptimality(t *testing.T) {
	for _, tc := range []struct{ m, n int }{{3, 2}, {100, 5}, {1000, 8}} {
		rng := rand.New(rand.NewSource(int64(tc.m*tc.n + 1)))
		a, bv := gen(rng, tc.m*tc.n), gen(rng, tc.m)
		s := New()
		ok(t, s.Factor(a, tc.m, tc.n))
		x, err := s.Solve(bv, tc.m)
		ok(t, err)
		for _, v := range atRes(a, x, bv, tc.m, tc.n) {
			if math.Abs(v) > 1e-9 {
				t.Fatalf("%+v: Aᵀ(Ax−b) component %v", tc, v)
			}
		}
	}
}

// Invariant 3: m different right-hand sides (100/1000/10000) in random
// arrival order never recompute the Gram; the counter stays 1.
func TestFactorOnceGramCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		rng := rand.New(rand.NewSource(int64(m)))
		s := New()
		ok(t, s.Factor(gen(rng, m*4), m, 4))
		for _, k := range rng.Perm(m) {
			_, err := s.Solve(gen(rand.New(rand.NewSource(int64(k)*131+7)), m), m)
			ok(t, err)
			if s.gramCount.Load() != 1 {
				t.Fatalf("m=%d rhs=%d: Gram computed %d times", m, k, s.gramCount.Load())
			}
		}
	}
}

// Invariant 4: rejected ops leave no state; the four sentinels differ.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if ErrDimMismatch == ErrUnderdetermined || ErrDimMismatch == ErrEmptySystem || ErrDimMismatch == ErrRankDeficient || ErrUnderdetermined == ErrEmptySystem || ErrUnderdetermined == ErrRankDeficient || ErrEmptySystem == ErrRankDeficient {
		t.Fatal("the four failure sentinels must be pairwise distinct")
	}
	good, dup := []float64{1, 1, 1, 2, 1, 3}, []float64{1, 1, 1, 1, 1, 1}
	s := New()
	if err := s.Factor(dup, 3, 2); !errors.Is(err, ErrRankDeficient) || s.g != nil {
		t.Fatalf("rejected pre-Factor left state behind: %v", err)
	}
	ok(t, s.Factor(good, 3, 2))
	bv := []float64{2, 3, 5}
	want, _ := s.Solve(bv, 3)
	good[0] = 99
	if still, _ := s.Solve(bv, 3); !reflect.DeepEqual(still, want) {
		t.Fatal("Factor must copy A rather than retain the caller slice")
	}
	calls := []struct {
		w error
		f func() error
	}{
		{ErrEmptySystem, func() error { return s.Factor(good, 0, 2) }},
		{ErrEmptySystem, func() error { return s.Factor(good, 3, 0) }},
		{ErrUnderdetermined, func() error { return s.Factor(good, 2, 3) }},
		{ErrDimMismatch, func() error { return s.Factor(good[:5], 3, 2) }},
		{ErrRankDeficient, func() error { return s.Factor(dup, 3, 2) }},
		{ErrDimMismatch, func() error { _, e := s.Solve(bv[:2], 3); return e }},
		{ErrDimMismatch, func() error { _, e := s.Solve(bv, 4); return e }},
	}
	for i, c := range calls {
		if err := c.f(); !errors.Is(err, c.w) {
			t.Fatalf("reject %d: %v", i, err)
		}
		got, e := s.Solve(bv, 3)
		if e != nil || !reflect.DeepEqual(got, want) || s.gramCount.Load() != 1 {
			t.Fatalf("reject %d changed usable state: %v", i, e)
		}
	}
	if _, err := New().Solve(bv, 3); !errors.Is(err, ErrNotFactored) {
		t.Fatalf("Solve before Factor: %v", err)
	}
}

// N goroutines solve one rhs on the read-only Gram, released together by
// a channel barrier (no sleeps): byte-identical, count still 1.
func TestConcurrentSolveIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	s := New()
	ok(t, s.Factor(gen(rng, 3000), 500, 6))
	bv := gen(rng, 500)
	const N = 48
	res := make([][]float64, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i], _ = s.Solve(bv, 500) // nil on error fails DeepEqual below
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(res[0], res[i]) {
			t.Fatalf("goroutine %d result differs byte-for-byte", i)
		}
	}
	if s.gramCount.Load() != 1 {
		t.Fatalf("Gram computed %d times under concurrency", s.gramCount.Load())
	}
}

func TestSelfCheck(t *testing.T) { ok(t, New().SelfCheck()) }
