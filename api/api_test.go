package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/lpath"
	"ontology/wdag"
)

var edges7 = [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}

func build(t *testing.T, n int, edges [][3]int64) *api.Graph {
	g, _ := api.New(n)
	for _, e := range edges {
		if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}
func naiveAll(n int, edges [][3]int64) (best int64, seq []int, have bool) {
	adj := make([][][3]int64, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e)
	}
	var dfs func(u int64, sum int64, path []int)
	dfs = func(u int64, sum int64, path []int) {
		if len(path) > 1 && (!have || sum > best || sum == best && slices.Compare(path, seq) < 0) {
			best, have, seq = sum, true, slices.Clone(path)
		}
		for _, e := range adj[u] {
			dfs(e[1], sum+e[2], append(path, int(e[1])))
		}
	}
	for v := range n {
		dfs(int64(v), 0, []int{v})
	}
	return
}
func TestSolveAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(755))
	for _, n := range []int{2, 5, 8} {
		for trial := 0; trial < 40; trial++ {
			perm := rng.Perm(n)
			var edges [][3]int64
			for i := 0; i < n; i++ {
				for j := i + 1; j < n; j++ {
					if rng.Intn(2) == 0 {
						edges = append(edges, [3]int64{int64(perm[i]), int64(perm[j]), int64(rng.Intn(21) - 10)})
					}
				}
			}
			rng.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
			total, path, err := build(t, n, edges).Solve()
			wantW, wantPath, have := naiveAll(n, edges)
			if !have && errors.Is(err, lpath.ErrNoPath) {
				continue
			}
			if err != nil || !have || total != wantW || !slices.Equal(path, wantPath) {
				t.Fatalf("n=%d trial=%d: got (%d,%v,%v), want (%d,%v)", n, trial, total, path, err, wantW, wantPath)
			}
		}
	}
}
func TestSolvePathLegal(t *testing.T) {
	for _, c := range []struct {
		n     int
		edges [][3]int64
	}{
		{5, edges7},
		{2, [][3]int64{{0, 1, -5}}},
		{7, append(slices.Clone(edges7), [3]int64{5, 6, 100})},
		{4, [][3]int64{{0, 1, 0}, {1, 2, 0}, {0, 2, 0}}},
	} {
		total, path, err := build(t, c.n, c.edges).Solve()
		wantW, wantPath, have := naiveAll(c.n, c.edges)
		if err != nil || !have || total != wantW || !slices.Equal(path, wantPath) {
			t.Fatalf("n=%d: got (%d,%v,%v), want (%d,%v)", c.n, total, path, err, wantW, wantPath)
		}
	}
}
func TestRejectedOpsKeepState(t *testing.T) {
	for _, n := range []int{0, -3} {
		if _, err := api.New(n); !errors.Is(err, wdag.ErrBadN) {
			t.Fatalf("n=%d 应报 ErrBadN", n)
		}
	}
	g := build(t, 4, [][3]int64{{0, 1, 5}})
	for _, c := range []struct {
		u, v int
		want error
	}{
		{-1, 2, wdag.ErrNodeRange}, {0, 4, wdag.ErrNodeRange}, {2, 2, wdag.ErrSelfLoop}, {0, 1, wdag.ErrDupEdge},
	} {
		if err := g.AddEdge(c.u, c.v, 1); !errors.Is(err, c.want) {
			t.Fatalf("AddEdge(%d,%d) 应报 %v", c.u, c.v, c.want)
		}
	}
	if g.EdgeCount() != 1 {
		t.Fatal("拒绝后 EdgeCount 改变")
	}
	if _, _, err := g.Solve(); err != nil {
		t.Fatalf("拒绝后不可用: %v", err)
	}
	_, _, cycErr := build(t, 3, [][3]int64{{0, 1, 1}, {1, 2, 1}, {2, 0, 1}}).Solve()
	_, _, noPathErr := build(t, 3, nil).Solve()
	if !errors.Is(cycErr, wdag.ErrCycle) || !errors.Is(noPathErr, lpath.ErrNoPath) {
		t.Fatal("含环/无边错误不符")
	}
	errs := []error{wdag.ErrBadN, wdag.ErrNodeRange, wdag.ErrSelfLoop, wdag.ErrDupEdge, wdag.ErrCycle, lpath.ErrNoPath}
	for i, e := range errs {
		if slices.Contains(errs[:i], e) {
			t.Fatal("哨兵错误不互不相同")
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if err := build(t, 1, nil).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestConcurrentSolve(t *testing.T) {
	g := build(t, 5, edges7)
	wantW, wantPath, _ := g.Solve()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if total, path, err := g.Solve(); err != nil || total != wantW || !slices.Equal(path, wantPath) {
				bad.Store(true)
			}
			if g.EdgeCount() != len(edges7) || i%16 == 0 && g.SelfCheck() != nil {
				bad.Store(true)
			}
		}(i)
	}
	wg.Wait()
	if bad.Load() {
		t.Error("并发结果不一致")
	}
}
