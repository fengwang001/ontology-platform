// Package api 对外暴露 DAG 拓扑排序能力，并发安全。依赖 topo。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dag"
	"ontology/topo"
)

// API 是并发安全的拓扑排序入口。
type API struct {
	mu sync.RWMutex
	g  *dag.Graph
}

// New 创建 n 个节点的 API，n 在此固定。
func New(n int) *API { return &API{g: dag.New(n)} }

// AddEdge 登记有向边 u→v，只登记不排序；非法边整体拒绝且不留痕。
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v)
}

// TopoSort 返回最小编号优先的拓扑序；含环时整体失败，返回 dag.ErrCycle。
func (a *API) TopoSort() ([]int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return new(topo.Sorter).Sort(a.g)
}

// EdgeCount 返回已登记的边数。
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

// SelfCheck 对一组内置图序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	g := dag.New(7)
	edges := [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return err
		}
	}
	got, err := new(topo.Sorter).Sort(g)
	if err != nil {
		return err
	}
	// 不变量 1：每条边 u→v 满足 pos(u) < pos(v)
	pos := make([]int, g.N())
	for i, v := range got {
		pos[v] = i
	}
	for _, e := range edges {
		if pos[e[0]] >= pos[e[1]] {
			return fmt.Errorf("selfcheck: edge %v violated in %v", e, got)
		}
	}
	// 不变量 2：与朴素参照逐元素一致
	if want := naive(g); !equal(got, want) {
		return fmt.Errorf("selfcheck: %v != naive %v", got, want)
	}
	// 不变量 3：含环整体失败，无部分序列
	c := dag.New(2)
	c.AddEdge(0, 1)
	c.AddEdge(1, 0)
	if out, err := new(topo.Sorter).Sort(c); !errors.Is(err, dag.ErrCycle) || out != nil {
		return fmt.Errorf("selfcheck: cycle not detected: %v %v", out, err)
	}
	// 不变量 4：被拒操作不改变已登记状态
	before := g.EdgeCount()
	if g.AddEdge(0, 0) == nil || g.AddEdge(0, 9) == nil || g.AddEdge(2, 0) == nil {
		return fmt.Errorf("selfcheck: bad edge accepted")
	}
	if g.EdgeCount() != before {
		return fmt.Errorf("selfcheck: rejected edge mutated state")
	}
	return nil
}

// naive 是每步全表扫描取最小编号的朴素 Kahn，作为参照实现。
func naive(g *dag.Graph) []int {
	n := g.N()
	indeg := make([]int, n)
	done := make([]bool, n)
	for v := 0; v < n; v++ {
		indeg[v] = g.Indeg(v)
	}
	var out []int
	for len(out) < n {
		for v := 0; v < n; v++ {
			if !done[v] && indeg[v] == 0 {
				done[v] = true
				out = append(out, v)
				for _, w := range g.Adj(v) {
					indeg[w]--
				}
				break
			}
		}
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
