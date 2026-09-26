package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// build 按给定顺序加边构造图。
func build(t *testing.T, n int, edges [][3]int64) *Graph {
	t.Helper()
	g, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	for _, e := range edges {
		if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
			t.Fatalf("AddEdge(%d,%d,%d): %v", e[0], e[1], e[2], err)
		}
	}
	return g
}

// TestMinCutMatchesBruteForce 钉住不变量 1、2：逐值等于枚举二划分的朴素参照。
func TestMinCutMatchesBruteForce(t *testing.T) {
	fixed := []struct {
		n     int
		edges [][3]int64
	}{
		{4, [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}}}, // 第三节图
		{3, [][3]int64{{0, 1, 2}, {1, 2, 3}, {0, 2, 4}}},            // 合并求和关键
		{5, [][3]int64{{0, 1, 7}, {2, 3, 9}}},                       // 孤立节点 → 0
		{2, [][3]int64{{0, 1, 42}}},
		{6, nil}, // 无边图 → 0
	}
	for _, c := range fixed {
		got, _ := build(t, c.n, c.edges).MinCut()
		if want := bruteForce(c.n, c.edges); got != want {
			t.Fatalf("n=%d edges=%v: got %d, want %d", c.n, c.edges, got, want)
		}
	}
	// 随机图 + 随机加边顺序，多档规模循环生成。
	for _, tc := range []struct{ n, p, seed int }{
		{2, 5, 1}, {4, 4, 2}, {6, 3, 3}, {8, 3, 4}, {9, 2, 5}, {9, 5, 6},
	} {
		rng := rand.New(rand.NewSource(int64(tc.seed)))
		var edges [][3]int64
		for u := 0; u < tc.n; u++ {
			for v := u + 1; v < tc.n; v++ {
				if rng.Intn(10) < tc.p {
					edges = append(edges, [3]int64{int64(u), int64(v), int64(1 + rng.Intn(20))})
				}
			}
		}
		rng.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
		got, _ := build(t, tc.n, edges).MinCut()
		if want := bruteForce(tc.n, edges); got != want {
			t.Fatalf("tc=%+v: got %d, want %d", tc, got, want)
		}
	}
}

// TestRejectErrors 四类故障注入各有可判定且互不相同的哨兵错误。
func TestRejectErrors(t *testing.T) {
	g := build(t, 3, [][3]int64{{0, 1, 5}})
	if _, err := New(1); !errors.Is(err, ErrTooFewNodes) {
		t.Fatalf("New(1) = %v", err)
	}
	cases := []struct {
		u, v int
		w    int64
		want error
	}{
		{0, 3, 1, ErrNodeOutOfRange},
		{2, 2, 1, ErrSelfLoop},
		{1, 0, 9, ErrDuplicateEdge}, // 与 (0,1) 同一对无向节点
	}
	seen := map[error]bool{}
	for _, c := range cases {
		err := g.AddEdge(c.u, c.v, c.w)
		if !errors.Is(err, c.want) {
			t.Fatalf("AddEdge(%d,%d,%d) = %v, want %v", c.u, c.v, c.w, err, c.want)
		}
		if seen[c.want] {
			t.Fatalf("sentinel %v reused across categories", c.want)
		}
		seen[c.want] = true
	}
}

// TestRejectedOpsKeepState 钉住不变量 4：被拒操作不改状态，图仍可正常使用。
func TestRejectedOpsKeepState(t *testing.T) {
	g := build(t, 4, [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}})
	before, _ := g.MinCut()
	for _, op := range []func() error{
		func() error { return g.AddEdge(0, 4, 1) },
		func() error { return g.AddEdge(3, 3, 1) },
		func() error { return g.AddEdge(1, 0, 7) },
		func() error { return g.AddEdge(0, 1, 0) },
	} {
		if op() == nil {
			t.Fatal("invalid op accepted")
		}
	}
	after, _ := g.MinCut()
	if g.EdgeCount() != 4 || before != after {
		t.Fatalf("state changed: edges=%d cut %d->%d", g.EdgeCount(), before, after)
	}
	if err := g.AddEdge(0, 3, 11); err != nil || g.EdgeCount() != 5 {
		t.Fatal("graph unusable after rejections")
	}
}

// TestConcurrentMinCut 并发读结果一致，-race 干净；不用 sleep。
func TestConcurrentMinCut(t *testing.T) {
	g := build(t, 4, [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}})
	const P = 16
	var wg sync.WaitGroup
	res := make([]int64, P)
	for i := 0; i < P; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := g.MinCut()
			if err != nil {
				t.Error(err)
			}
			res[i] = c
			_ = g.EdgeCount()
			_ = g.SelfCheck()
		}(i)
	}
	wg.Wait()
	for i := range res {
		if res[i] != 1 {
			t.Fatalf("goroutine %d got %d, want 1", i, res[i])
		}
	}
}

// TestSelfCheck 内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	g := build(t, 2, [][3]int64{{0, 1, 1}})
	if err := g.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
