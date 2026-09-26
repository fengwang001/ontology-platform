package gauss

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

func identity(n int) []float64 {
	m := make([]float64, n*n)
	for i := 0; i < n; i++ {
		m[i*n+i] = 1
	}
	return m
}

// 对角矩阵的乘减行操作计数必须恒为 0，不随 n 增长。
func TestDiagZeroMulSub(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n)
		for i := 0; i < n; i++ {
			a[i*n+i] = float64(i) + 2
		}
		before := mulSubOps.Load()
		inv, err := Invert(a, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := mulSubOps.Load() - before; got != 0 {
			t.Fatalf("n=%d: mulSubOps delta = %d, want 0", n, got)
		}
		for i := 0; i < n; i++ {
			if want := 1 / (float64(i) + 2); inv[i*n+i] != want {
				t.Fatalf("n=%d: inv[%d][%d] = %v, want %v", n, i, i, inv[i*n+i], want)
			}
		}
	}
}

// 消元完成时左半必须精确等于单位阵（IEEE 精确，非近似）。
func TestLeftHalfExactIdentity(t *testing.T) {
	cases := []struct {
		name string
		n    int
		a    []float64
	}{
		{"spec-2x2", 2, []float64{0, 2, 1, 1}},
		{"neg-zeros-3x3", 3, []float64{2, 0, 1, -1, 3, 0, 0, -2, 4}},
		{"perm-4x4", 4, []float64{0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 1, 0, 0, 0}},
		{"dense-4x4", 4, []float64{4, 1, 2, 0, 1, 3, 0, 1, 2, 0, 5, 1, 0, 1, 1, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			L := slices.Clone(tc.a)
			R := identity(tc.n)
			if _, err := eliminate(L, R, tc.n); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(L, identity(tc.n)) {
				t.Fatalf("left half = %v, want exact I", L)
			}
		})
	}
}

// 计数器确实在数乘减行操作：spec 2x2 恰好 1 次（k=1 的上消元）。
func TestCounterCountsOps(t *testing.T) {
	before := mulSubOps.Load()
	if _, err := Invert([]float64{0, 2, 1, 1}, 2); err != nil {
		t.Fatal(err)
	}
	if got := mulSubOps.Load() - before; got != 1 {
		t.Fatalf("mulSubOps delta = %d, want 1", got)
	}
}

// 被拒操作不留痕：计数器不变，哨兵错误可区分，之后仍正常。
func TestFailureNoTrace(t *testing.T) {
	before := mulSubOps.Load()
	cases := []struct {
		name string
		a    []float64
		n    int
		want error
	}{
		{"dim-mismatch", make([]float64, 3), 2, ErrDim},
		{"empty", nil, 0, ErrEmpty},
		{"negative-n", nil, -3, ErrEmpty},
		{"singular", []float64{1, 2, 2, 4}, 2, ErrSingular},
		{"singular-zero-col", []float64{0, 1, 0, 1}, 2, ErrSingular},
	}
	for _, tc := range cases {
		if _, err := Invert(tc.a, tc.n); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := mulSubOps.Load(); got != before {
		t.Fatalf("mulSubOps changed by rejected calls: %d -> %d", before, got)
	}
	if _, err := Invert([]float64{0, 2, 1, 1}, 2); err != nil {
		t.Fatalf("unusable after rejections: %v", err)
	}
}

// 并发：N 个 goroutine 对同一份只读输入调用 Invert，结果逐字节相同；
// 同时给原子计数器制造竞争（go test -race 验证）。
func TestConcurrentInv(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	shared := make([]float64, 30*30)
	for i := range shared {
		shared[i] = rng.Float64() - 0.5
		if i/30 == i%30 {
			shared[i] += 30
		}
	}
	results := make([][]float64, 32)
	var wg sync.WaitGroup
	for w := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv, err := Invert(shared, 30)
			if err != nil {
				t.Error(err)
			}
			results[w] = inv
		}()
	}
	wg.Wait()
	for w := range results[1:] {
		if !slices.Equal(results[w+1], results[0]) {
			t.Fatalf("goroutine %d result differs", w+1)
		}
	}
}
