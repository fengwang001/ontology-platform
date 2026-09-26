// Package api 对外门面：New/AddEdge/TopoSort/EdgeCount/SelfCheck。依赖 topo。
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/dag"
	"ontology/topo"
)

// 对外可判定哨兵错误（与 dag/topo 的哨兵是同一批，互不相同）。
var (
	ErrSelfLoop       = dag.ErrSelfLoop
	ErrNodeOutOfRange = dag.ErrNodeOutOfRange
	ErrDuplicateEdge  = dag.ErrDuplicateEdge
	ErrCycle          = topo.ErrCycle
)

// Graph 是对外的图句柄，方法均可并发调用。
type Graph struct {
	g *dag.Graph
}

// New 创建 n 个节点的图，n 固定不变。
func New(n int) *Graph { return &Graph{g: dag.New(n)} }

// AddEdge 登记边 u→v；非法边被拒绝且不改状态。
func (a *Graph) AddEdge(u, v int) error { return a.g.AddEdge(u, v) }

// TopoSort 返回最小编号优先拓扑序；含环时返回 ErrCycle 且无部分序列。
func (a *Graph) TopoSort() ([]int, error) { return topo.Sort(a.g) }

// EdgeCount 返回已登记边数。
func (a *Graph) EdgeCount() int { return a.g.EdgeCount() }

// SelfCheck 对一组内置图序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量1+2+3：合法拓扑序、与朴素参照逐元素一致、完整含全部节点。
	cases := []struct {
		n     int
		edges [][2]int
	}{
		{7, [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}}},
		{4, [][2]int{{3, 0}, {3, 1}, {2, 1}, {1, 0}}},
		{5, nil},
		{1, nil},
	}
	for i, c := range cases {
		g := New(c.n)
		for _, e := range c.edges {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				return fmt.Errorf("selfcheck case %d build: %w", i, err)
			}
		}
		got, err := g.TopoSort()
		if err != nil {
			return fmt.Errorf("selfcheck case %d sort: %w", i, err)
		}
		if err := verifyOrder(c.n, c.edges, got); err != nil {
			return fmt.Errorf("selfcheck case %d: %w", i, err)
		}
		if want := naiveKahn(c.n, c.edges); !slices.Equal(got, want) {
			return fmt.Errorf("selfcheck case %d: got %v, naive %v", i, got, want)
		}
	}
	// 不变量3：含环整体失败，无部分序列。
	cyc := New(2)
	_ = cyc.AddEdge(0, 1)
	_ = cyc.AddEdge(1, 0)
	if got, err := cyc.TopoSort(); !errors.Is(err, ErrCycle) || got != nil {
		return fmt.Errorf("selfcheck: cycle not detected (got %v, err %v)", got, err)
	}
	// 不变量4：被拒操作不改状态，且图仍可正常使用。
	bad := []error{ErrSelfLoop, ErrNodeOutOfRange, ErrDuplicateEdge}
	g := New(3)
	_ = g.AddEdge(0, 1)
	attempts := []error{g.AddEdge(2, 2), g.AddEdge(0, 9), g.AddEdge(0, 1)}
	for i, err := range attempts {
		if !errors.Is(err, bad[i]) || g.EdgeCount() != 1 {
			return fmt.Errorf("selfcheck: rejection %d leaked state", i)
		}
	}
	if _, err := g.TopoSort(); err != nil {
		return fmt.Errorf("selfcheck: graph unusable after rejections: %w", err)
	}
	return nil
}

// verifyOrder 核验序列是含全部节点的合法拓扑序（不变量1+3）。
func verifyOrder(n int, edges [][2]int, order []int) error {
	if len(order) != n {
		return fmt.Errorf("length %d != %d", len(order), n)
	}
	pos := make([]int, n)
	for i, v := range order {
		if v < 0 || v >= n {
			return fmt.Errorf("node %d out of range", v)
		}
		pos[v] = i
	}
	seen := map[int]bool{}
	for _, v := range order {
		if seen[v] {
			return fmt.Errorf("node %d duplicated", v)
		}
		seen[v] = true
	}
	for _, e := range edges {
		if pos[e[0]] >= pos[e[1]] {
			return fmt.Errorf("edge %d->%d violated", e[0], e[1])
		}
	}
	return nil
}

// naiveKahn 朴素参照：每步全表扫描取编号最小的入度 0 节点（不变量2）。
func naiveKahn(n int, edges [][2]int) []int {
	indeg := make([]int, n)
	adj := make([][]int, n)
	for _, e := range edges {
		indeg[e[1]]++
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	done := make([]bool, n)
	var out []int
	for len(out) < n {
		v := -1
		for i := 0; i < n; i++ {
			if !done[i] && indeg[i] == 0 {
				v = i
				break
			}
		}
		if v < 0 {
			return out
		}
		done[v] = true
		out = append(out, v)
		for _, w := range adj[v] {
			indeg[w]--
		}
	}
	return out
}
