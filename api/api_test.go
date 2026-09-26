package api_test

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func bits(x []float64) []uint64 {
	b := make([]uint64, len(x))
	for i, f := range x {
		b[i] = math.Float64bits(f)
	}
	return b
}

func matvec(a, v []float64, n int) []float64 {
	w := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			w[i] += a[i*n+j] * v[j]
		}
	}
	return w
}

func TestEigenConvergence(t *testing.T) {
	eng, err := api.New(1e-13, 100000)
	if err != nil {
		t.Fatal(err)
	}
	// 一步精确收敛矩阵，残差严格为 0；其特征值分别为 {2,0}、{-4,0}、{6,0,0}，|λ| 严格最大。
	for i, c := range []struct {
		a, v0 []float64
		want  float64
	}{
		{[]float64{1, 1, 1, 1}, []float64{1, 0}, 2},
		{[]float64{-2, 2, 2, -2}, []float64{0, 1}, -4},
		{[]float64{3, 0, -3, 0, 0, 0, -3, 0, 3}, []float64{1, 0, 0}, 6},
	} {
		n := len(c.v0)
		lam, v, err := eng.Eigen(c.a, c.v0, n)
		w := matvec(c.a, v, n)
		ok := err == nil && math.Abs(lam-c.want) <= 1e-12 && math.Abs(lam) > 0
		for j := range v { // A·v == λ·v，每项残差 ≤1e-9
			ok = ok && math.Abs(w[j]-lam*v[j]) <= 1e-9
		}
		if !ok {
			t.Fatalf("case %d lam=%v err=%v", i, lam, err)
		}
	}
	// 多步收敛：特征值 {4,2}，收敛到模最大的 4（受 float64 Rayleigh 判据地板限制，仅核特征值）。
	if lam, _, err := eng.Eigen([]float64{3, 1, 1, 3}, []float64{1, 0}, 2); err != nil ||
		math.Abs(lam-4) > 1e-9 || math.Abs(lam) <= 2 {
		t.Fatalf("multi-step lam=%v err=%v", lam, err)
	}
}

func TestEigenInputUnchanged(t *testing.T) {
	eng, _ := api.New(1e-13, 100000)
	for i, c := range []struct{ a, v0 []float64 }{
		{[]float64{3, 1, 1, 3}, []float64{1, 0}},
		{[]float64{3, 0, -3, 0, 0, 0, -3, 0, 3}, []float64{1, 0, 0}},
	} {
		ba, bv := bits(c.a), bits(c.v0)
		eng.Eigen(c.a, c.v0, len(c.v0))
		if !slices.Equal(bits(c.a), ba) || !slices.Equal(bits(c.v0), bv) {
			t.Fatalf("case %d inputs mutated", i)
		}
	}
}

func TestEigenErrors(t *testing.T) {
	eng, _ := api.New(1e-9, 100)
	id := []float64{1, 0, 0, 1}
	for i, c := range []struct {
		a, v []float64
		n    int
		want error
	}{
		{make([]float64, 3), []float64{1, 0}, 2, api.ErrDimensionMismatch},
		{id, []float64{1}, 2, api.ErrDimensionMismatch},
		{id, nil, 0, api.ErrEmptySystem},
		{id, []float64{0, 0}, 2, api.ErrZeroStartVector},
	} {
		if _, _, err := eng.Eigen(c.a, c.v, c.n); !errors.Is(err, c.want) {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
	strict, _ := api.New(1e-30, 1)
	if _, _, err := strict.Eigen([]float64{3, 1, 1, 3}, []float64{1, 0}, 2); !errors.Is(err, api.ErrNotConverged) {
		t.Fatalf("not-converged err=%v", err)
	}
	if _, err := api.New(0, 1); !errors.Is(err, api.ErrNonPositiveTol) {
		t.Fatalf("tol err=%v", err)
	}
	if _, err := api.New(1e-9, 0); !errors.Is(err, api.ErrBadMaxIter) {
		t.Fatalf("maxIter err=%v", err)
	}
	if _, _, err := eng.Eigen([]float64{1, 1, 1, 1}, []float64{1, 0}, 2); err != nil { // 被拒后仍可用
		t.Fatalf("engine unusable after rejects: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	eng, err := api.New(1e-13, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentIdentical(t *testing.T) {
	eng, _ := api.New(1e-13, 100000)
	a, v0 := []float64{3, 1, 1, 3}, []float64{1, 0}
	const N = 64
	var wg sync.WaitGroup
	lams := make([]uint64, N)
	vecs := make([][]float64, N)
	wg.Add(N)
	for g := 0; g < N; g++ { // 并发读同一份输入，不用 sleep 制造时序
		go func(g int) {
			defer wg.Done()
			l, v, err := eng.Eigen(a, v0, 2)
			if err != nil {
				t.Errorf("goroutine %d: %v", g, err)
				return
			}
			lams[g], vecs[g] = math.Float64bits(l), v
		}(g)
	}
	wg.Wait()
	for g := 1; g < N; g++ { // 各自结果逐字节相同
		if lams[g] != lams[0] || !slices.Equal(bits(vecs[g]), bits(vecs[0])) {
			t.Fatalf("goroutine %d result differs", g)
		}
	}
	if !slices.Equal(bits(a), []uint64{math.Float64bits(3), math.Float64bits(1), math.Float64bits(1), math.Float64bits(3)}) ||
		bits(v0)[0] != math.Float64bits(1) || bits(v0)[1] != 0 {
		t.Fatal("shared inputs mutated under concurrency")
	}
}
