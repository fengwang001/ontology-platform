package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

const eps = 1e-9

func TestOrthogonalUpper(t *testing.T) {
	type tc struct {
		n int
		a []float64
	}
	cases := []tc{
		{1, []float64{5}},
		{1, []float64{-3}},
		{2, []float64{3, 1, 4, 1}},
		{3, []float64{1, 2, 3, 0, 4, 5, 0, -6, 7}}, // 列0下三角已全零→跳过
		{3, []float64{0, -1, 2, -3, 0, 4, 5, 6, -7}},
	}
	for _, sz := range []int{4, 6, 9} { // 随机矩阵用循环生成，含负/零/正
		rng := rand.New(rand.NewSource(int64(sz * 7919)))
		m := make([]float64, sz*sz)
		for i := range m {
			m[i] = math.Round(rng.NormFloat64() * 4) // 离散值，刻意制造精确零
		}
		cases = append(cases, tc{sz, m})
	}
	for _, c := range cases {
		e := api.New()
		src := append([]float64(nil), c.a...)
		Q, R, err := e.Factor(c.a, c.n)
		if err != nil {
			t.Fatalf("n=%d: unexpected error %v", c.n, err)
		}
		for i := 0; i < c.n; i++ {
			for j := 0; j < c.n; j++ {
				var qtq, recon float64 // 不变量2: QᵀQ=I；不变量1: Q·R=A
				for p := 0; p < c.n; p++ {
					qtq += Q[p*c.n+i] * Q[p*c.n+j]
					recon += Q[i*c.n+p] * R[p*c.n+j]
				}
				if math.Abs(qtq-b2f(i == j)) > eps {
					t.Fatalf("n=%d: QᵀQ[%d][%d]=%v", c.n, i, j, qtq)
				}
				if math.Abs(recon-src[i*c.n+j]) > eps {
					t.Fatalf("n=%d: QR[%d][%d]=%v want %v", c.n, i, j, recon, src[i*c.n+j])
				}
				if i > j && R[i*c.n+j] != 0 { // R 严格上三角（对角线以下逐字节为0）
					t.Fatalf("n=%d: R[%d][%d]=%v, want 0", c.n, i, j, R[i*c.n+j])
				}
			}
		}
		for i := range c.a { // 输入只读
			if c.a[i] != src[i] {
				t.Fatalf("n=%d: input mutated at %d", c.n, i)
			}
		}
	}
}

func TestConcurrentDeterministic(t *testing.T) {
	const N = 32
	e := api.New()
	a := []float64{3, 1, 0, -2, 4, 1, 5, 0, 0, 6, -3, 8, 1, 2, 9, -4}
	Qs := make([][]float64, N)
	Rs := make([][]float64, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	order := rand.New(rand.NewSource(20260926)).Perm(N) // 随机到达顺序，无 sleep
	for _, g := range order {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			Qs[g], Rs[g], errs[g] = e.Factor(a, 4)
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if errs[g] != nil {
			t.Fatalf("goroutine %d: %v", g, errs[g])
		}
		for i := range a { // 所有 goroutine 的 Q、R 必须逐字节相同
			if math.Float64bits(Qs[g][i]) != math.Float64bits(Qs[0][i]) ||
				math.Float64bits(Rs[g][i]) != math.Float64bits(Rs[0][i]) {
				t.Fatalf("goroutine %d differs at index %d", g, i)
			}
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	e := api.New()
	cases := []struct {
		a   []float64
		n   int
		err error
	}{
		{nil, 0, api.ErrEmpty},
		{nil, -2, api.ErrEmpty},
		{[]float64{1, 2, 3}, 2, api.ErrDimMismatch},
		{make([]float64, 10), 3, api.ErrDimMismatch},
		{[]float64{1, 0, 2, 0}, 2, api.ErrZeroTail}, // [[1,0],[2,0]] 列1全零
	}
	got := map[error]bool{}
	for _, c := range cases {
		a := append([]float64(nil), c.a...)
		if Q, R, err := e.Factor(c.a, c.n); !errors.Is(err, c.err) || Q != nil || R != nil {
			t.Fatalf("a=%v n=%d: err=%v want %v", c.a, c.n, err, c.err)
		}
		for i := range c.a {
			if c.a[i] != a[i] {
				t.Fatalf("rejected call mutated input at %d", i)
			}
		}
		got[c.err] = true
	}
	if len(got) != 3 { // 三类错误必须互不相同
		t.Fatalf("expected 3 distinct sentinel errors, got %d", len(got))
	}
	if _, _, err := e.Factor([]float64{3, 1, 4, 1}, 2); err != nil { // 拒绝后仍可用
		t.Fatalf("engine unusable after rejection: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
