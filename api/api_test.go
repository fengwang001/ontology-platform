package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

// randDAG 生成 n 个节点的随机 DAG：随机置换 π，只加 π 中前→后的边。
func randDAG(r *rand.Rand, n, m int) [][2]int {
	perm := r.Perm(n)
	var edges [][2]int
	seen := map[[2]int]bool{}
	for len(edges) < m && n > 1 {
		i, j := r.Intn(n), r.Intn(n)
		if i == j {
			continue
		}
		if i > j {
			i, j = j, i
		}
		e := [2]int{perm[i], perm[j]}
		if !seen[e] {
			seen[e] = true
			edges = append(edges, e)
		}
	}
	return edges
}

// TestTopoSortInvariants 钉住不变量1/2/3：多档规模 × 随机加边顺序。
func TestTopoSortInvariants(t *testing.T) {
	sizes := []struct{ n, m int }{{1, 0}, {2, 1}, {7, 7}, {50, 120}, {200, 800}}
	for _, s := range sizes {
		for seed := int64(0); seed < 5; seed++ {
			r := rand.New(rand.NewSource(seed*1000 + int64(s.n)))
			edges := randDAG(r, s.n, s.m)
			r.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
			g := New(s.n)
			for _, e := range edges {
				if err := g.AddEdge(e[0], e[1]); err != nil {
					t.Fatalf("AddEdge: %v", err)
				}
			}
			got, err := g.TopoSort()
			if err != nil {
				t.Fatalf("n=%d seed=%d: %v", s.n, seed, err)
			}
			if err := verifyOrder(s.n, edges, got); err != nil {
				t.Fatalf("n=%d seed=%d: %v", s.n, seed, err)
			}
			if want := naiveKahn(s.n, edges); !slices.Equal(got, want) {
				t.Fatalf("n=%d seed=%d: got %v, naive %v", s.n, seed, got, want)
			}
		}
	}
}

// TestRejections 钉住不变量4：四类故障可判定、互不相同、被拒后状态不变且仍可用。
func TestRejections(t *testing.T) {
	g := New(4)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.AddEdge(0, 1))
	must(g.AddEdge(2, 3))
	before := g.EdgeCount()
	rejects := []struct {
		name string
		err  error
		want error
	}{
		{"自环", g.AddEdge(2, 2), ErrSelfLoop},
		{"越界", g.AddEdge(0, 4), ErrNodeOutOfRange},
		{"重复边", g.AddEdge(0, 1), ErrDuplicateEdge},
	}
	for _, rj := range rejects {
		if !errors.Is(rj.err, rj.want) {
			t.Fatalf("%s: err=%v, want %v", rj.name, rj.err, rj.want)
		}
		if g.EdgeCount() != before {
			t.Fatalf("%s: 被拒后状态改变", rj.name)
		}
	}
	sents := []error{ErrSelfLoop, ErrNodeOutOfRange, ErrDuplicateEdge, ErrCycle}
	for i, e := range sents {
		if slices.Contains(sents[:i], e) {
			t.Fatal("哨兵错误不互不相同")
		}
	}
	if _, err := g.TopoSort(); err != nil { // 被拒后仍可正常使用
		t.Fatalf("TopoSort after rejections: %v", err)
	}
	// 含环：整体失败、可判定、不改已登记边集。
	must(g.AddEdge(1, 2))
	must(g.AddEdge(3, 0))
	if got, err := g.TopoSort(); !errors.Is(err, ErrCycle) || got != nil {
		t.Fatalf("cycle: got %v, err %v", got, err)
	}
	if g.EdgeCount() != before+2 {
		t.Fatal("环检测改变了边集")
	}
}

// TestSelfCheck 钉住自检方法本身可用。
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentTopoSort 钉住并发：N 个 goroutine 对同一不再加边的 DAG 并发读，结果逐元素相同，不用 sleep。
func TestConcurrentTopoSort(t *testing.T) {
	const n, m, goroutines = 100, 300, 32
	r := rand.New(rand.NewSource(42))
	edges := randDAG(r, n, m)
	g := New(n)
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	want, err := g.TopoSort()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([][]int, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := g.TopoSort()
			if err != nil || g.EdgeCount() != m || SelfCheck() != nil {
				t.Errorf("concurrent call failed: %v", err)
			}
			results[i] = got
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if !slices.Equal(got, want) {
			t.Fatalf("goroutine %d: %v != %v", i, got, want)
		}
	}
}
