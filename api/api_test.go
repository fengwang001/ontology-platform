package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/elim"
)

// 生成对角占优（必非奇异）的随机方程组。
func gen(n int, seed int64) ([]float64, []float64) {
	rng := rand.New(rand.NewSource(seed))
	a := make([]float64, n*n)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a[i*n+j] = rng.Float64()*2 - 1
		}
		a[i*n+i] += 3
		b[i] = rng.Float64()*2 - 1
	}
	return a, b
}

// 不变量1：残差 |A·x-b| 每项 ≤1e-9，多档规模表驱动。
func TestResidual(t *testing.T) {
	for _, n := range []int{1, 2, 3, 10, 50, 200} {
		a, b := gen(n, int64(n))
		x, err := api.New().Solve(a, b, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		for i := 0; i < n; i++ {
			r := -b[i]
			for j := 0; j < n; j++ {
				r += a[i*n+j] * x[j]
			}
			if math.Abs(r) > 1e-9 {
				t.Fatalf("n=%d row=%d residual=%g", n, i, r)
			}
		}
	}
}

// 不变量2：求解前后输入逐字节不变。
func TestInputUnchanged(t *testing.T) {
	a, b := gen(20, 7)
	aCopy, bCopy := append([]float64(nil), a...), append([]float64(nil), b...)
	if _, err := api.New().Solve(a, b, 20); err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(aCopy[i]) {
			t.Fatalf("a[%d] mutated", i)
		}
	}
	for i := range b {
		if math.Float64bits(b[i]) != math.Float64bits(bCopy[i]) {
			t.Fatalf("b[%d] mutated", i)
		}
	}
}

// 三类故障注入各有可判定且互不相同的哨兵错误。
func TestDistinctErrors(t *testing.T) {
	a, b := gen(3, 1)
	singular := []float64{1, 2, 3, 0, 0, 4, 0, 0, 5}
	cases := []struct {
		name string
		a, b []float64
		n    int
		want error
	}{
		{"空系统", a[:0], b[:0], 0, elim.ErrEmpty},
		{"a维度错", a[:8], b, 3, elim.ErrDimension},
		{"b维度错", a, b[:2], 3, elim.ErrDimension},
		{"奇异矩阵", singular, b, 3, elim.ErrSingular},
	}
	// 三类哨兵错误互不相同。
	if elim.ErrEmpty == elim.ErrDimension || elim.ErrDimension == elim.ErrSingular || elim.ErrEmpty == elim.ErrSingular {
		t.Fatal("sentinel errors must be distinct")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := api.New().Solve(c.a, c.b, c.n)
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
		})
	}
}

// 不变量4：被拒操作不改变输入，且之后仍可正常使用。
func TestRejectNoSideEffect(t *testing.T) {
	a, b := gen(4, 2)
	aCopy, bCopy := append([]float64(nil), a...), append([]float64(nil), b...)
	s := api.New()
	_, _ = s.Solve(a, b, 0)
	_, _ = s.Solve(a[:15], b, 4)
	_, _ = s.Solve([]float64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, b, 4)
	for i := range a {
		if a[i] != aCopy[i] {
			t.Fatalf("rejected call mutated a[%d]", i)
		}
	}
	for i := range b {
		if b[i] != bCopy[i] {
			t.Fatalf("rejected call mutated b[%d]", i)
		}
	}
	if _, err := s.Solve(a, b, 4); err != nil {
		t.Fatalf("solve after rejections: %v", err)
	}
}

// 并发：N 个 goroutine 对同一份输入求解，结果逐字节相同。
func TestConcurrentSolve(t *testing.T) {
	a, b := gen(30, 3)
	want, err := api.New().Solve(a, b, 30)
	if err != nil {
		t.Fatal(err)
	}
	const g = 32
	results := make([][]float64, g)
	var wg sync.WaitGroup
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			x, err := api.New().Solve(a, b, 30)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = x
		}(i)
	}
	wg.Wait()
	for i := 0; i < g; i++ {
		for j := range want {
			if math.Float64bits(results[i][j]) != math.Float64bits(want[j]) {
				t.Fatalf("goroutine %d x[%d] differs", i, j)
			}
		}
	}
}
