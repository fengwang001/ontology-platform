package api

import (
	"errors"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func newSolver(t *testing.T) *Solver {
	s, err := New(1e-12, 500)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var cases = []struct {
	name  string
	a, v0 []float64
	n     int
	lam   float64
}{
	{"2x2", []float64{3, 1, 1, 3}, []float64{1, 0}, 2, 4},
	{"neg", []float64{-3, 1, 1, -3}, []float64{1, 0}, 2, -4},
	{"diag4", []float64{5, 0, 0, 0, 0, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0, -4}, []float64{1, 1, 1, 1}, 4, 5},
}

// TestResidualAgainstNaive 钉不变量 1：A·v==λ·v 每项 ≤1e-9，λ 模最大。
func TestResidualAgainstNaive(t *testing.T) {
	s := newSolver(t)
	for _, c := range cases {
		lam, v, err := s.Eigen(c.a, c.v0, c.n)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for i := 0; i < c.n; i++ { // 朴素参照：逐分量核对 A·v == λ·v
			var av float64
			for j := 0; j < c.n; j++ {
				av += c.a[i*c.n+j] * v[j]
			}
			if d := math.Abs(av - lam*v[i]); d > 1e-9 {
				t.Fatalf("%s: residual[%d]=%v", c.name, i, d)
			}
		}
		if d := math.Abs(lam - c.lam); d > 1e-9 {
			t.Fatalf("%s: lam=%v want %v", c.name, lam, c.lam)
		}
	}
}

// TestNormalizationDeterminism 钉不变量 2：max|v_i|==1，重复调用逐位一致。
func TestNormalizationDeterminism(t *testing.T) {
	s := newSolver(t)
	for _, c := range cases {
		lam1, v1, err1 := s.Eigen(c.a, c.v0, c.n)
		lam2, v2, err2 := s.Eigen(c.a, c.v0, c.n)
		if err1 != nil || err2 != nil || lam1 != lam2 {
			t.Fatalf("%s: %v %v lam %v!=%v", c.name, err1, err2, lam1, lam2)
		}
		var mx float64
		for _, x := range v1 {
			mx = math.Max(mx, math.Abs(x))
		}
		if !slices.Equal(v1, v2) || mx != 1 {
			t.Fatalf("%s: 不一致或 max|v|=%v", c.name, mx)
		}
	}
}

// TestInputsUnmodified 钉不变量 3：Eigen 前后 a、v0 逐字节不变。
func TestInputsUnmodified(t *testing.T) {
	s := newSolver(t)
	for _, c := range cases {
		a, v0 := append([]float64(nil), c.a...), append([]float64(nil), c.v0...)
		if _, _, err := s.Eigen(a, v0, c.n); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(a, c.a) || !slices.Equal(v0, c.v0) {
			t.Fatalf("%s: input mutated", c.name)
		}
	}
}

// TestSentinelErrorsDistinct 钉不变量 4 与第五节：四类错误互不相同、
// 被拒后仍可用；顺带验证 New 参数校验与 SelfCheck。
func TestSentinelErrorsDistinct(t *testing.T) {
	s := newSolver(t)
	a := cases[0].a
	ss, _ := New(1e-15, 1) // maxIter=1 必不收敛
	errs := []error{
		pickErr(s.Eigen([]float64{1, 2, 3}, []float64{1, 0}, 2)),
		pickErr(s.Eigen(nil, nil, 0)),
		pickErr(s.Eigen(a, []float64{0, 0}, 2)),
		pickErr(ss.Eigen(a, []float64{1, 0}, 2)),
	}
	want := []error{ErrDimMismatch, ErrEmptySystem, ErrZeroVector, ErrNoConvergence}
	seen := map[error]bool{}
	for i := range errs {
		if !errors.Is(errs[i], want[i]) || seen[errs[i]] {
			t.Fatalf("case %d: %v want %v（互不相同）", i, errs[i], want[i])
		}
		seen[errs[i]] = true
	}
	if _, _, err := s.Eigen(a, []float64{1, 0}, 2); err != nil {
		t.Fatalf("被拒后不可用: %v", err)
	}
	if _, err := New(0, 10); !errors.Is(err, ErrInvalidTol) {
		t.Fatal("New 应拒 tol=0")
	}
	if _, err := New(1e-9, 0); !errors.Is(err, ErrInvalidMaxIter) {
		t.Fatal("New 应拒 maxIter=0")
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func pickErr(_ float64, _ []float64, err error) error { return err }

// TestConcurrentIdentical 钉第六节：并发对同一输入调 Eigen，结果逐字节相同。不用 sleep。
func TestConcurrentIdentical(t *testing.T) {
	s := newSolver(t)
	a, v0 := cases[0].a, cases[0].v0
	wantL, wantV, err := s.Eigen(a, v0, 2)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for k := 0; k < 32; k++ {
		wg.Go(func() {
			<-start
			l, v, err := s.Eigen(a, v0, 2)
			if err != nil || l != wantL || !slices.Equal(v, wantV) {
				bad.Store(true)
			}
		})
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发结果不逐字节一致")
	}
}
