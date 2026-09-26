package api

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/lufact"
	"ontology/solve"
)

// TestReconstructA covers invariant 1 plus Forward/Backward on the n=3 case.
func TestReconstructA(t *testing.T) {
	for _, a := range fixedA {
		n := dimOf(a)
		L, U, err := lufact.Factor(a, n)
		if err != nil || !closeVec(matProd(L, U, n), a, 1e-9) {
			t.Fatalf("reconstruct n=%d err=%v", n, err)
		}
	}
	r := rand.New(rand.NewSource(7))
	for n := 1; n <= 5; n++ {
		for range 20 {
			a, L0, U0 := synth(n, r)
			L, U, err := lufact.Factor(a, n)
			if err != nil || !closeVec(L, L0, 1e-9) || !closeVec(U, U0, 1e-9) {
				t.Fatalf("n=%d factor mismatch err=%v", n, err)
			}
		}
	}
	y := solve.Forward([]float64{1, 0, 0, 2, 1, 0, 4, 3, 1}, []float64{7, 19, 49}, 3)
	x := solve.Backward([]float64{2, 1, 1, 0, 1, 1, 0, 0, 2}, y, 3)
	if !closeVec(y, []float64{7, 5, 6}, 0) || !closeVec(x, []float64{1, 2, 3}, 0) {
		t.Fatalf("forward/backward y=%v x=%v", y, x)
	}
}

// Invariant 2: L is unit lower triangular; U is upper triangular.
func TestTriangularShape(t *testing.T) {
	for _, a := range fixedA {
		n := dimOf(a)
		L, U, _ := lufact.Factor(a, n)
		for i := range n {
			for j := range n {
				if (i == j && L[i*n+j] != 1) ||
					(i < j && L[i*n+j] != 0) || (i > j && U[i*n+j] != 0) {
					t.Fatalf("shape n=%d i=%d j=%d", n, i, j)
				}
			}
		}
	}
}

// Invariant 3: one factorization serves m RHS; the counter stays exactly 1.
func TestFactorCountStaysOne(t *testing.T) {
	const n = 4
	a, _, _ := synth(n, rand.New(rand.NewSource(1)))
	for _, m := range []int{100, 1000, 10000} {
		e := New()
		if err := e.Factor(a, n); err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewSource(int64(m)))
		for range m {
			b := make([]float64, n)
			for j := range b {
				b[j] = r.NormFloat64() * 3
			}
			x, err := e.Solve(b, n)
			if err != nil || maxResid(a, x, b, n) > 1e-9 || e.factorCount.Load() != 1 {
				t.Fatalf("m=%d count=%d err=%v", m, e.factorCount.Load(), err)
			}
		}
	}
}

// Invariant 4: rejected calls are total failures and leave no state behind.
func TestErrorsLeaveNoTrace(t *testing.T) {
	sents := []error{ErrDimension, ErrEmpty, ErrZeroPivot, ErrNotFactored}
	e := New()
	zp := []float64{1, 2, 3, 2, 4, 6, 7, 8, 9} // second pivot becomes 0
	cases := []struct {
		want error
		call func() error
	}{
		{ErrEmpty, func() error { return e.Factor(nil, 0) }},
		{ErrDimension, func() error { return e.Factor([]float64{1, 2, 3}, 2) }},
		{ErrZeroPivot, func() error { return e.Factor(zp, 3) }},
		{ErrNotFactored, func() error { _, err := e.Solve([]float64{1}, 1); return err }},
	}
	for _, c := range cases {
		err := c.call()
		if !errors.Is(err, c.want) {
			t.Fatalf("want %v got %v", c.want, err)
		}
		for _, s := range sents {
			if s != c.want && errors.Is(err, s) {
				t.Fatalf("ambiguous error %v", err)
			}
		}
		if e.L != nil || e.factorCount.Load() != 0 {
			t.Fatal("rejected call changed state")
		}
	}
	if err := e.Factor([]float64{2, 1, 1, 4, 3, 3, 8, 7, 9}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Solve([]float64{1, 2}, 3); !errors.Is(err, ErrDimension) {
		t.Fatal("bad b length must be ErrDimension")
	}
	x, err := e.Solve([]float64{7, 19, 49}, 3)
	if err != nil || !closeVec(x, []float64{1, 2, 3}, 1e-9) || e.factorCount.Load() != 1 {
		t.Fatalf("engine not reusable after rejections: %v %v", x, err)
	}
}

// Concurrent Solve calls are read-only and must return byte-identical results.
func TestConcurrentSolveByteIdentical(t *testing.T) {
	const n = 4
	a, _, _ := synth(n, rand.New(rand.NewSource(99)))
	e := New()
	if err := e.Factor(a, n); err != nil {
		t.Fatal(err)
	}
	b := []float64{1.5, -2.25, 3, -0.5}
	const N = 64
	res := make([][]float64, N)
	var wg sync.WaitGroup
	for g := range N {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], _ = e.Solve(b, n) }(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ {
		if !bytes.Equal(encF64(res[0]), encF64(res[g])) {
			t.Fatal("concurrent solutions are not byte-identical")
		}
	}
	if e.factorCount.Load() != 1 || maxResid(a, res[0], b, n) > 1e-9 {
		t.Fatal("counter or residual wrong after concurrency")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
