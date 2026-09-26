package api

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/gram"
	"ontology/lsq"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// TestNaiveConsistency 钉不变量 1：Aᵀ(Ax−b) 每项 ≤1e-9（残差最小），且输入 A、b 不改。
func TestNaiveConsistency(t *testing.T) {
	for _, sz := range [][2]int{{5, 1}, {50, 2}, {300, 4}} {
		m, n, a := sz[0], sz[1], vander(sz[0], sz[1])
		a0, b := slices.Clone(a), randVec(m, 42, 10)
		b0 := slices.Clone(b)
		s := New()
		must(t, s.Factor(a, m, n))
		x, err := s.Solve(b, m)
		must(t, err)
		if normalResidual(a, x, b, m, n) > 1e-9 {
			t.Fatal("normal-equation residual > 1e-9")
		}
		if !slices.Equal(a, a0) || !slices.Equal(b, b0) {
			t.Fatal("input A or b modified")
		}
	}
}

// TestGramSymmetric 钉不变量 2：Gram 逐位对称，SolveG 按完整对称矩阵求解（解为全 1）。
func TestGramSymmetric(t *testing.T) {
	cases := []struct {
		a    []float64
		m, n int
	}{
		{[]float64{1, 1, 1, 2, 1, 3}, 3, 2},
		{[]float64{-2, 0, 3, 1, -1, 0, 2, 2, -4}, 3, 3},
		{vander(30, 4), 30, 4},
	}
	for _, tc := range cases {
		g := gram.Gram(tc.a, tc.m, tc.n)
		if !symmetric(g, tc.n) {
			t.Fatalf("Gram not symmetric for m=%d n=%d", tc.m, tc.n)
		}
		for _, v := range lsq.SolveG(g, onesRHS(g, tc.n), tc.n) {
			if math.Abs(v-1) > 1e-9 {
				t.Fatalf("symmetric solve gave non-unit %g", v)
			}
		}
	}
}

// TestFactorOnceGramCount 钉不变量 3：固定 A，m=100/1000/10000 个不同右端，计数恒 1。
func TestFactorOnceGramCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		must(t, s.Factor(vander(m, 3), m, 3))
		b := make([]float64, m)
		for k := 0; k < m; k++ {
			b[k] = float64((k*7 + m) % 101)
			must(t, func() error { _, e := s.Solve(b, m); return e }())
		}
		if s.gramCountForTest() != 1 {
			t.Fatalf("m=%d gram count != 1", m)
		}
	}
}

// TestRejectedLeavesNoTrace 钉不变量 4：四类哨兵互不相同，被拒后缓存/计数不变且可继续。
func TestRejectedLeavesNoTrace(t *testing.T) {
	s := New()
	must(t, s.Factor(vander(20, 3), 20, 3))
	b := make([]float64, 20)
	cases := []struct {
		av   []float64
		m, n int
		want error
	}{
		{[]float64{1}, 20, 3, ErrDimMismatch},
		{[]float64{1, 2}, 1, 2, ErrUnderdetermined},
		{nil, 0, 0, ErrEmpty},
		{[]float64{1, 1, 2, 2, 3, 3, 4, 4, 5, 5}, 5, 2, ErrRankDeficient},
	}
	rand.New(rand.NewSource(7)).Shuffle(len(cases), func(i, j int) { cases[i], cases[j] = cases[j], cases[i] })
	for _, tc := range cases {
		if err := s.Factor(tc.av, tc.m, tc.n); !errors.Is(err, tc.want) {
			t.Fatalf("case %+v: %v", tc, err)
		}
		if s.gramCountForTest() != 1 {
			t.Fatal("rejected Factor changed gram count")
		}
		if _, err := s.Solve(b, 20); err != nil {
			t.Fatalf("unusable after rejection: %v", err)
		}
	}
	if !distinctErrs(ErrDimMismatch, ErrUnderdetermined, ErrEmpty, ErrRankDeficient) {
		t.Fatal("sentinels not distinct")
	}
	if _, err := s.Solve([]float64{1}, 20); !errors.Is(err, ErrDimMismatch) {
		t.Fatal("len(b) mismatch not rejected")
	}
	if err := New().Factor(nil, 0, 2); !errors.Is(err, ErrEmpty) {
		t.Fatal("empty n>0 not rejected")
	}
	if _, err := New().Solve(b, 20); !errors.Is(err, ErrNotFactored) {
		t.Fatal("fresh solver leaked state")
	}
}

// TestConcurrentSolveIdentical：N 个 goroutine 同一右端，解逐字节相同，无 sleep。
func TestConcurrentSolveIdentical(t *testing.T) {
	m, s := 64, New()
	must(t, s.Factor(vander(m, 4), m, 4))
	b := randVec(m, 99, 3)
	const N = 64
	res := make([][]float64, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) { defer wg.Done(); res[idx], _ = s.Solve(b, m) }(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		for j := range res[0] {
			if math.Float64bits(res[0][j]) != math.Float64bits(res[i][j]) {
				t.Fatalf("goroutine %d differs at %d", i, j)
			}
		}
	}
}
