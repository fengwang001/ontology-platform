package api_test

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/mul"
)

// randMatrix 生成含负零正的行主序矩阵，固定种子可复现。
func randMatrix(n, seed int) []float64 {
	r := rand.New(rand.NewSource(int64(seed)))
	m := make([]float64, n*n)
	for i := range m {
		m[i] = r.Float64()*20 - 10
	}
	return m
}

// bitsEqual 逐位比较两个等长 float64 切片。
func bitsEqual(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

// TestBlockedMatchesNaive 钉住不变量 1：多档 n/b（含整除与尾块、负零正），
// Blocked 与 Naive 必须逐位相等。
func TestBlockedMatchesNaive(t *testing.T) {
	cases := []struct {
		n  int
		bs []int
	}{
		{1, []int{1}}, {2, []int{1, 2}}, {5, []int{1, 2, 3, 5}},
		{6, []int{2, 3, 4, 6}}, {17, []int{1, 7, 17}}, {31, []int{2, 5, 16, 31}},
	}
	for ci, c := range cases {
		a, b := randMatrix(c.n, ci+1), randMatrix(c.n, ci+101)
		want := mul.Naive(a, b, c.n)
		for _, bs := range c.bs {
			if got := mul.Blocked(a, b, c.n, bs); !bitsEqual(got, want) {
				t.Fatalf("n=%d b=%d Blocked 与 Naive 非逐位相等", c.n, bs)
			}
		}
	}
}

// TestBlockedDeterministic 钉住不变量 3：同一输入重复调用结果逐位相同。
func TestBlockedDeterministic(t *testing.T) {
	for _, c := range []struct{ n, b int }{{5, 2}, {33, 7}} {
		a, b := randMatrix(c.n, 7), randMatrix(c.n, 9)
		first := mul.Blocked(a, b, c.n, c.b)
		for rep := 0; rep < 5; rep++ {
			if !bitsEqual(mul.Blocked(a, b, c.n, c.b), first) {
				t.Fatalf("n=%d b=%d 第 %d 次调用结果不稳定", c.n, c.b, rep)
			}
		}
	}
}

// TestRejectionLeavesNoTrace 钉住不变量 4 与第五节：三类哨兵互不相同、
// 可判定，且被拒后状态不变、引擎可继续正常使用。
func TestRejectionLeavesNoTrace(t *testing.T) {
	n := 7
	a, b := randMatrix(n, 3), randMatrix(n, 4)
	e := api.New(3)
	baseline, err := e.Mul(a, b, n)
	if err != nil {
		t.Fatalf("基线调用失败: %v", err)
	}
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"空矩阵 n=0", func() error { _, err := e.Mul(a, b, 0); return err }, api.ErrEmptyMatrix},
		{"空矩阵 n<0", func() error { _, err := e.Mul(a, b, -3); return err }, api.ErrEmptyMatrix},
		{"块过小 b=0", func() error { _, err := api.New(0).Mul(a, b, n); return err }, api.ErrBlockSize},
		{"块为负", func() error { _, err := api.New(-1).Mul(a, b, n); return err }, api.ErrBlockSize},
		{"块过大 b>n", func() error { _, err := api.New(8).Mul(a, b, n); return err }, api.ErrBlockSize},
		{"a 维度不符", func() error { _, err := e.Mul(a[:n*n-1], b, n); return err }, api.ErrShapeMismatch},
		{"b 维度不符", func() error { _, err := e.Mul(a, b[:n*n-1], n); return err }, api.ErrShapeMismatch},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if errors.Is(api.ErrEmptyMatrix, api.ErrBlockSize) ||
		errors.Is(api.ErrEmptyMatrix, api.ErrShapeMismatch) ||
		errors.Is(api.ErrBlockSize, api.ErrShapeMismatch) {
		t.Fatal("三类哨兵必须互不相同")
	}
	// 全部拒绝之后：同一引擎结果必须与基线逐位相同（状态未被污染）。
	after, err := e.Mul(a, b, n)
	if err != nil || !bitsEqual(after, baseline) {
		t.Fatalf("拒绝后状态被污染: err=%v", err)
	}
}

// TestMulConcurrentEqual 钉住第六节：N 个 goroutine 并发对同一份输入
// 调用 Mul，结果必须逐字节相同；用栅栏代替 sleep 制造并发。
func TestMulConcurrentEqual(t *testing.T) {
	n := 23
	a, b := randMatrix(n, 5), randMatrix(n, 6)
	e := api.New(4)
	want, err := e.Mul(a, b, n)
	if err != nil {
		t.Fatalf("基线失败: %v", err)
	}
	const g = 16
	results := make([][]float64, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], _ = e.Mul(a, b, n) // 输入已验证合法
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < g; i++ {
		if !bitsEqual(results[i], want) {
			t.Fatalf("goroutine %d 结果与基线非逐字节相同", i)
		}
	}
}

// TestSelfCheck 钉住第一节：自检方法真实核验四条不变量。
func TestSelfCheck(t *testing.T) {
	for _, b := range []int{1, 2, 3, 5} {
		if err := api.New(b).SelfCheck(); err != nil {
			t.Fatalf("SelfCheck(b=%d): %v", b, err)
		}
	}
}
