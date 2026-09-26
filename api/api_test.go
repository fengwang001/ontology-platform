package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dgraph"
)

// specEdges 是第三节的七条边；配 n=8 时节点 7 不可达。
var specEdges = [][2]int{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}}

func build(t *testing.T, n int, edges [][2]int) *api.Engine {
	t.Helper()
	e, err := api.New(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, ed := range edges {
		if err := e.AddEdge(ed[0], ed[1]); err != nil {
			t.Fatal(err)
		}
	}
	e.Compute()
	return e
}

func TestSpecGraph(t *testing.T) {
	e := build(t, 8, specEdges)
	for v, want := range []int{0, 0, 1, 2, 2, 2, 0, -1} {
		if got, err := e.IDom(v); err != nil || got != want {
			t.Fatalf("IDom(%d)=%d,%v want %d", v, got, err, want)
		}
	}
	cases := []struct {
		a, b int
		want bool
	}{
		{0, 6, true}, {1, 6, false}, {2, 5, true}, {3, 5, false},
		{0, 7, false}, {7, 7, false}, {6, 6, true}, {0, 0, true},
	}
	for _, c := range cases {
		if got := e.Dominates(c.a, c.b); got != c.want {
			t.Fatalf("Dominates(%d,%d)=%v want %v", c.a, c.b, got, c.want)
		}
	}
	if _, err := e.IDom(99); !errors.Is(err, dgraph.ErrNodeRange) {
		t.Fatalf("IDom(99) err=%v, want ErrNodeRange", err)
	}
}

func TestRejectedOpsKeepState(t *testing.T) {
	for _, n := range []int{0, -3} {
		if _, err := api.New(n); !errors.Is(err, dgraph.ErrNonPositive) {
			t.Fatalf("New(%d)=%v, want ErrNonPositive", n, err)
		}
	}
	e := build(t, 4, [][2]int{{0, 1}, {1, 2}})
	cases := []struct {
		u, v int
		want error
	}{
		{0, 4, dgraph.ErrNodeRange},
		{-1, 2, dgraph.ErrNodeRange},
		{2, 2, dgraph.ErrSelfLoop},
		{0, 1, dgraph.ErrDuplicate},
	}
	for _, c := range cases {
		if err := e.AddEdge(c.u, c.v); !errors.Is(err, c.want) {
			t.Fatalf("AddEdge(%d,%d)=%v, want %v", c.u, c.v, err, c.want)
		}
		if e.EdgeCount() != 2 {
			t.Fatalf("rejected AddEdge(%d,%d) changed EdgeCount", c.u, c.v)
		}
	}
	// 四类故障的哨兵两两不同
	_, errN := api.New(0)
	cats := []error{errN, e.AddEdge(0, 4), e.AddEdge(2, 2), e.AddEdge(0, 1)}
	for i, ei := range cats {
		for _, ej := range cats[i+1:] {
			if errors.Is(ei, ej) {
				t.Fatalf("sentinels not distinct: %v / %v", ei, ej)
			}
		}
	}
	e.Compute()
	if !e.Dominates(0, 2) || e.Dominates(1, 0) {
		t.Fatal("graph unusable after rejections")
	}
}

func TestConcurrentDominates(t *testing.T) {
	e := build(t, 8, specEdges)
	pairs := [][2]int{{0, 6}, {1, 6}, {2, 5}, {3, 5}, {0, 0}, {6, 6}, {0, 7}, {4, 5}}
	want := make([]bool, len(pairs))
	for i, p := range pairs {
		want[i] = e.Dominates(p[0], p[1])
	}
	const G = 32
	got := make([][]bool, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]bool, len(pairs))
			for i, p := range pairs {
				got[g][i] = e.Dominates(p[0], p[1])
			}
			_, _ = e.IDom(g % 8)
			_ = e.EdgeCount()
			_ = e.SelfCheck()
		}(g)
	}
	wg.Wait()
	for g := 0; g < G; g++ {
		for i := range want {
			if got[g][i] != want[i] {
				t.Fatalf("goroutine %d pair %v: %v != %v", g, pairs[i], got[g][i], want[i])
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	e, err := api.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
