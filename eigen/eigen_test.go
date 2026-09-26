package eigen

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// diagFamily 构造 A=diag(3,1,0,…,0)、v0 全 1：间隙 1/3 与 n 无关。
func diagFamily(n int) ([]float64, []float64) {
	a, v0 := make([]float64, n*n), make([]float64, n)
	a[0] = 3
	if n > 1 {
		a[n+1] = 1
	}
	for i := range v0 {
		v0[i] = 1
	}
	return a, v0
}

func residual(a, v []float64, n int, lam float64) (r float64) {
	for i := 0; i < n; i++ {
		var s float64
		for j := 0; j < n; j++ {
			s += a[i*n+j] * v[j]
		}
		r = math.Max(r, math.Abs(s-lam*v[i]))
	}
	return
}

func TestIterateKnown(t *testing.T) {
	cases := []struct {
		name  string
		a, v0 []float64
		n     int
		lam   float64
	}{
		{"2x2", []float64{3, 1, 1, 3}, []float64{1, 0}, 2, 4},
		{"neg-dominant", []float64{-3, 1, 1, -3}, []float64{1, 0}, 2, -4},
		{"diag-mixed", []float64{5, 0, 0, 0, 0, 2, 0, 0, 0, 0, 1, 0, 0, 0, 0, -4},
			[]float64{1, 1, 1, 1}, 4, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lam, v, _, err := Iterate(c.a, c.v0, c.n, 1e-12, 200)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(lam-c.lam) > 1e-9 {
				t.Fatalf("lam=%v want %v", lam, c.lam)
			}
			if r := residual(c.a, v, c.n, lam); r > 1e-9 {
				t.Fatalf("residual=%v", r)
			}
		})
	}
}

// TestIterationsIndependentOfN 钉第四节：固定间隙族轮数不随 n 增长。
func TestIterationsIndependentOfN(t *testing.T) {
	ns := []int{100, 1000, 10000}
	var base int
	const bound = 120 // 与 n 无关的小常数（实际约 60）
	for i, n := range ns {
		a, v0 := diagFamily(n)
		lam, _, it, err := Iterate(a, v0, n, 1e-9, 500)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if math.Abs(lam-3) > 1e-9 {
			t.Fatalf("n=%d lam=%v", n, lam)
		}
		if it > bound {
			t.Fatalf("n=%d iters=%d > %d（随 n 增长？）", n, it, bound)
		}
		if i == 0 {
			base = it
		} else if it != base {
			t.Fatalf("n=%d iters=%d 与 n=%d 的 %d 不一致", n, it, ns[0], base)
		}
	}
}

// TestCounterDeltaAndRejected 钉第四、五节：计数器增量恰为真实乘法次数，拒绝零触碰。
func TestCounterDeltaAndRejected(t *testing.T) {
	a, v0 := diagFamily(64)
	before := matVecCalls.Load()
	_, _, it, err := Iterate(a, v0, 64, 1e-9, 500)
	if err != nil {
		t.Fatal(err)
	}
	if d := matVecCalls.Load() - before; int64(it) != d || d <= 0 {
		t.Fatalf("iters=%d 计数器增量=%d", it, d)
	}
	rejected := []struct {
		a, v0   []float64
		n, maxI int
		tol     float64
		want    error
	}{
		{[]float64{1, 2, 3}, []float64{1, 0}, 2, 10, 1e-9, ErrDimMismatch},
		{a, make([]float64, 63), 64, 10, 1e-9, ErrDimMismatch},
		{nil, nil, 0, 10, 1e-9, ErrEmptySystem},
		{a, make([]float64, 64), 64, 10, 1e-9, ErrZeroVector},
		{a, v0, 64, 10, 0, ErrInvalidTol},
		{a, v0, 64, 10, -1, ErrInvalidTol},
		{a, v0, 64, 0, 1e-9, ErrInvalidMaxIter},
	}
	stamp := matVecCalls.Load()
	for i, c := range rejected {
		if _, _, _, err := Iterate(c.a, c.v0, c.n, c.tol, c.maxI); !errors.Is(err, c.want) {
			t.Fatalf("case %d: err=%v want %v", i, err, c.want)
		}
	}
	if got := matVecCalls.Load(); got != stamp {
		t.Fatalf("拒绝路径触碰计数器：%d → %d", stamp, got)
	}
	if _, _, _, err := Iterate(a, v0, 64, 1e-15, 1); !errors.Is(err, ErrNoConvergence) {
		t.Fatalf("不收敛: err=%v", err) // maxIter=1 必不达 tol
	}
}

// TestSymmetricRandom 随机对称矩阵（含负零正），残差达标。
func TestSymmetricRandom(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		r := rand.New(rand.NewSource(seed))
		n := 12
		a := make([]float64, n*n)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				x := r.Float64()*20 - 10
				a[i*n+j], a[j*n+i] = x, x
			}
		}
		v0 := make([]float64, n)
		for i := range v0 {
			v0[i] = r.Float64()*2 - 1
		}
		lam, v, _, err := Iterate(a, v0, n, 1e-10, 2000)
		if err != nil {
			t.Fatalf("seed=%d: %v", seed, err)
		}
		if res := residual(a, v, n, lam); res > 1e-9 {
			t.Fatalf("seed=%d residual=%v", seed, res)
		}
	}
}
