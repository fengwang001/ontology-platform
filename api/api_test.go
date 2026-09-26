package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dag"
)

// naive 是每步全表扫描取最小编号的朴素 Kahn 参照实现。
func naive(n int, edges [][2]int) []int {
	indeg := make([]int, n)
	for _, e := range edges {
		indeg[e[1]]++
	}
	done := make([]bool, n)
	var out []int
	for len(out) < n {
		for v := 0; v < n; v++ {
			if done[v] || indeg[v] != 0 {
				continue
			}
			done[v] = true
			out = append(out, v)
			for _, e := range edges {
				if e[0] == v {
					indeg[e[1]]--
				}
			}
			break
		}
	}
	return out
}

// TestTopoOrderInvariants 钉住不变量 1/2/3：合法拓扑序、与朴素参照一致、完整无重。
func TestTopoOrderInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 7, 50, 200} {
		for trial := 0; trial < 5; trial++ {
			var edges [][2]int
			for u := 0; u < n; u++ {
				for v := u + 1; v < n; v++ {
					if rng.Intn(4) == 0 {
						edges = append(edges, [2]int{u, v})
					}
				}
			}
			rng.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
			a := api.New(n)
			for _, e := range edges {
				_ = a.AddEdge(e[0], e[1]) // u<v<n 且无重复，必然成功
			}
			got, err := a.TopoSort()
			if err != nil {
				t.Fatalf("n=%d: %v", n, err)
			}
			pos := make([]int, n)
			for i, v := range got {
				pos[v] = i
			}
			for _, e := range edges {
				if pos[e[0]] >= pos[e[1]] {
					t.Fatalf("n=%d: edge %v violated in %v", n, e, got)
				}
			}
			if want := naive(n, edges); !slices.Equal(got, want) {
				t.Fatalf("n=%d: %v != naive %v", n, got, want)
			}
		}
	}
}

// TestFaultInjection 钉住不变量 3/4：四类错误可判定且互不相同，被拒后状态不变。
func TestFaultInjection(t *testing.T) {
	a := api.New(3)
	if err := a.AddEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	before := a.EdgeCount()
	cases := []struct{ u, v int }{{1, 1}, {-1, 2}, {0, 3}, {0, 1}}
	wants := []error{dag.ErrSelfLoop, dag.ErrNodeRange, dag.ErrNodeRange, dag.ErrDupEdge}
	for i, c := range cases {
		if err := a.AddEdge(c.u, c.v); !errors.Is(err, wants[i]) {
			t.Errorf("AddEdge(%d,%d): got %v want %v", c.u, c.v, err, wants[i])
		}
	}
	errs := []error{dag.ErrSelfLoop, dag.ErrNodeRange, dag.ErrDupEdge, dag.ErrCycle}
	for i, e1 := range errs {
		for j, e2 := range errs {
			if i != j && errors.Is(e1, e2) {
				t.Errorf("sentinels %d/%d not distinct", i, j)
			}
		}
	}
	_, err1 := a.TopoSort()
	unchanged := a.EdgeCount() == before
	err2 := a.AddEdge(1, 2)
	if !unchanged || err1 != nil || err2 != nil {
		t.Fatal("state changed or graph unusable after rejections")
	}
	b := api.New(2)
	b.AddEdge(0, 1)
	b.AddEdge(1, 0)
	if out, err := b.TopoSort(); !errors.Is(err, dag.ErrCycle) || out != nil {
		t.Fatalf("cycle: got %v %v", out, err)
	}
}

// TestConcurrentTopoSort 并发调用结果必须逐元素相同。
func TestConcurrentTopoSort(t *testing.T) {
	a := api.New(64)
	for u := 0; u < 64; u++ {
		for v := u + 1; v < 64; v++ {
			if (u+v)%3 == 0 {
				a.AddEdge(u, v)
			}
		}
	}
	want, _ := a.TopoSort()
	start := make(chan struct{})
	res := make([][]int, 16)
	var wg sync.WaitGroup
	for p := range res {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			res[p], _ = a.TopoSort()
		}(p)
	}
	close(start)
	wg.Wait()
	for p, r := range res {
		if !slices.Equal(r, want) {
			t.Fatalf("goroutine %d: %v != %v", p, r, want)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New(1).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
