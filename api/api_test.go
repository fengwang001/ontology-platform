package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/bmg"
	"ontology/km"
)

func matcherOf(t *testing.T, w [][]int64) *Matcher {
	t.Helper()
	m, _ := New(len(w))
	for l := range w {
		for r, v := range w[l] {
			if e := m.SetWeight(l, r, v); e != nil {
				t.Fatal(e)
			}
		}
	}
	return m
}
func randW(rng *rand.Rand, n int, span int64) [][]int64 {
	w := make([][]int64, n)
	for i := range w {
		w[i] = make([]int64, n)
		for j := range w[i] {
			w[i][j] = rng.Int63n(span) - span/2
		}
	}
	return w
}

func TestSolveMatchesBruteForce(t *testing.T) { // 不变量 1/2/3 + 字典序最小，对拍包内朴素枚举 brute
	cases := [][][]int64{
		{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}},
		{{-5, -2}, {-3, -4}},
		{{1, 1}, {1, 1}},
		{{7}},
		{{1, 2, 3}, {6, 5, 4}, {7, 9, 8}},
	}
	rng := rand.New(rand.NewSource(42))
	for n := 1; n <= 6; n++ { // 随机权矩阵循环生成
		for k := 0; k < 30; k++ {
			cases = append(cases, randW(rng, n, 41))
		}
	}
	for ci, w := range cases {
		sum, mt, e := matcherOf(t, w).Solve()
		bs, bm := brute(w)
		if e != nil || sum != bs || !slices.Equal(mt, bm) {
			t.Fatalf("case %d: (%d,%v,%v) want (%d,%v)", ci, sum, mt, e, bs, bm)
		}
	}
	sum, mt, _ := matcherOf(t, cases[0]).Solve() // 题三具体数值钉死
	if sum != 28 || !slices.Equal(mt, []int{1, 0, 2}) {
		t.Fatalf("题三应 28/[1 0 2]，得 %d/%v", sum, mt)
	}
}
func TestSelfCheck(t *testing.T) {
	if e := SelfCheck(); e != nil {
		t.Fatal(e)
	}
}

func TestRejectionLeavesStateUnchanged(t *testing.T) { // 不变量 4：四类哨兵可判、互异，被拒不落盘、仍可用
	for _, badN := range []int{0, -2} {
		if _, e := New(badN); !errors.Is(e, ErrInvalidN) {
			t.Fatalf("New(%d)=%v", badN, e)
		}
	}
	m, _ := New(2)
	if e := m.SetWeight(0, 0, 1); e != nil {
		t.Fatal(e)
	}
	bad := []struct {
		l, r int
		w    int64
		e    error
	}{{2, 0, 1, bmg.ErrNodeRange}, {0, -1, 1, bmg.ErrNodeRange}, {0, 0, 9, bmg.ErrDuplicate}}
	for _, c := range bad {
		if e := m.SetWeight(c.l, c.r, c.w); !errors.Is(e, c.e) {
			t.Fatalf("(%d,%d)=%v want %v", c.l, c.r, e, c.e)
		}
	}
	if _, _, e := m.Solve(); !errors.Is(e, ErrIncomplete) {
		t.Fatalf("未设全=%v", e)
	}
	for _, c := range [][3]int{{0, 1, 3}, {1, 0, 4}, {1, 1, 5}} { // 补全 [[1,3],[4,5]]
		if e := m.SetWeight(c[0], c[1], int64(c[2])); e != nil {
			t.Fatal(e)
		}
	}
	sum, mt, e := m.Solve() // 最优 [1,0]=7，证明被拒操作无污染
	if e != nil || sum != 7 || !slices.Equal(mt, []int{1, 0}) {
		t.Fatalf("被拒后异常：%d %v %v", sum, mt, e)
	}
	seen := map[string]bool{}
	for _, e := range []error{ErrInvalidN, bmg.ErrNodeRange, bmg.ErrDuplicate, ErrIncomplete} {
		if seen[e.Error()] {
			t.Fatal("哨兵错误必须互不相同")
		}
		seen[e.Error()] = true
	}
}

func TestDeltaRChecksBounded(t *testing.T) { // n=100..10000，每次求 delta 检查右节点数 ≤2，不随 n 线性增长
	if !km.VerifyDeltaBound([]int{100, 1000, 10000}, 2) {
		t.Fatal("求 delta 检查右节点数超过 2，疑似全表扫描")
	}
}

func TestConcurrentSolveConsistent(t *testing.T) { // N goroutine 对冻结矩阵并发 Solve，逐元素相同；无 sleep
	rng := rand.New(rand.NewSource(7))
	n := 80
	m := matcherOf(t, randW(rng, n, 1000))
	const N = 32
	var wg sync.WaitGroup
	sums := make([]int64, N)
	ms := make([][]int, N)
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			sums[g], ms[g], _ = m.Solve()
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if sums[g] != sums[0] || !slices.Equal(ms[g], ms[0]) {
			t.Fatalf("goroutine %d 与 0 不一致", g)
		}
	}
}
