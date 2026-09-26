// Package api 是对外门面：有向图传递闭包的登记、计算与查询。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dg"
	"ontology/tc"
)

// API 持有图与其传递闭包。读方法（Reach/EdgeCount/SelfCheck）可并发调用。
type API struct {
	mu sync.RWMutex
	g  *dg.Graph
	c  *tc.Closure // Compute 前为 nil
}

// New 创建 n 个节点的空实例；n 非正返回 dg.ErrNonPositiveN。
func New(n int) (*API, error) {
	g, err := dg.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g: g}, nil
}

func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v)
}

func (a *API) Compute() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.c = tc.Compute(a.g)
}

// Reach 报告是否存在从 i 到 j 的、至少含 1 条边的有向路径；Compute 前返回 false。
func (a *API) Reach(i, j int) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.c == nil {
		return false
	}
	return a.c.Reach(i, j)
}

func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

type graphSpec struct {
	n     int
	edges [][2]int
}

func build(s graphSpec) (*API, error) {
	a, err := New(s.n)
	if err != nil {
		return nil, err
	}
	for _, e := range s.edges {
		if err := a.AddEdge(e[0], e[1]); err != nil {
			return nil, err
		}
	}
	a.Compute()
	return a, nil
}

// naiveClosure 朴素参照：对每个 i 做 DFS，标记距离 ≥1 的可达节点。
func naiveClosure(s graphSpec) [][]bool {
	adj := make([][]int, s.n)
	for _, e := range s.edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	out := make([][]bool, s.n)
	for src := range out {
		seen := make([]bool, s.n)
		stack := append([]int(nil), adj[src]...)
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !seen[v] {
				seen[v] = true
				stack = append(stack, adj[v]...)
			}
		}
		out[src] = seen
	}
	return out
}

// SelfCheck 对一组内置图（含环、链、孤立点、空图）核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	specs := []graphSpec{
		{5, [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}}},
		{4, [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{3, [][2]int{{0, 1}, {0, 2}, {1, 2}}},
		{4, nil}, {1, nil},
	}
	for _, s := range specs {
		a, err := build(s)
		if err != nil {
			return err
		}
		naive := naiveClosure(s)
		for i := 0; i < s.n; i++ {
			for j := 0; j < s.n; j++ {
				if a.Reach(i, j) != naive[i][j] { // 不变量 1、2：定义正确且与朴素参照一致
					return fmt.Errorf("selfcheck: n=%d Reach(%d,%d)=%v, naive=%v", s.n, i, j, a.Reach(i, j), naive[i][j])
				}
				for k := 0; k < s.n; k++ { // 不变量 3：传递性
					if a.Reach(i, k) && a.Reach(k, j) && !a.Reach(i, j) {
						return fmt.Errorf("selfcheck: transitivity broken at (%d,%d,%d)", i, k, j)
					}
				}
			}
		}
	}
	return checkFailureAtomicity() // 不变量 4
}

func checkFailureAtomicity() error { // 不变量 4
	if _, err := New(0); !errors.Is(err, dg.ErrNonPositiveN) {
		return fmt.Errorf("selfcheck: New(0) err=%v", err)
	}
	a, err := build(graphSpec{3, [][2]int{{0, 1}}})
	if err != nil {
		return err
	}
	uv := [][2]int{{0, 3}, {1, 1}, {0, 1}}
	want := []error{dg.ErrNodeOutOfRange, dg.ErrSelfLoop, dg.ErrDuplicateEdge}
	for k := range uv {
		if err := a.AddEdge(uv[k][0], uv[k][1]); !errors.Is(err, want[k]) {
			return fmt.Errorf("selfcheck: AddEdge(%d,%d) err=%v, want %v", uv[k][0], uv[k][1], err, want[k])
		}
	}
	if a.EdgeCount() != 1 || a.AddEdge(1, 2) != nil { // 状态不变且仍可正常使用
		return fmt.Errorf("selfcheck: state changed or reuse broken")
	}
	return nil
}
