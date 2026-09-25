// Package topo 提供基于三色标记 DFS 的拓扑排序与环检测。
package topo

import (
	"errors"
	"fmt"

	"ontology/graph"
)

// 哨兵错误，可用 errors.Is 区分。
var (
	ErrCycle   = errors.New("topo: dependency cycle")
	ErrBadEdge = errors.New("topo: edge endpoint out of range")
)

// CycleError 携带一个环上节点。
type CycleError struct{ Node int }

func (e *CycleError) Error() string { return fmt.Sprintf("topo: cycle through node %d", e.Node) }
func (e *CycleError) Unwrap() error { return ErrCycle }

// Edge 是一条有向边 U→V（U 依赖先于 V）。
type Edge struct{ U, V int }

// 三色标记：白=未访问，灰=在当前递归栈中，黑=已完成。
const (
	white = iota
	gray
	black
)

// TopoSort 返回满足所有边 u→v（u 在 v 前）的拓扑序；有环返回 ErrCycle。
// 同输入多次调用返回相同 order，且无可变共享状态，并发安全。
func TopoSort(n int, edges []Edge) ([]int, error) {
	order, _, err := run(n, edges)
	return order, err
}

// EdgeVisits 返回同样输入下排序的边访问次数，用于验证 O(n+m)。
func EdgeVisits(n int, edges []Edge) int {
	_, visits, _ := run(n, edges)
	return visits
}

// run 中 visits 为非导出计数器：每条边恰访问一次。
func run(n int, edges []Edge) ([]int, int, error) {
	if n < 0 {
		return nil, 0, fmt.Errorf("%w: n=%d", ErrBadEdge, n)
	}
	g := graph.New(n)
	for _, e := range edges {
		if e.U < 0 || e.U >= n || e.V < 0 || e.V >= n {
			return nil, 0, fmt.Errorf("%w: %d->%d (n=%d)", ErrBadEdge, e.U, e.V, n)
		}
		g.AddEdge(e.U, e.V)
	}
	color := make([]int, n)
	order := make([]int, 0, n)
	visits := 0
	var dfs func(u int) error
	dfs = func(u int) error {
		color[u] = gray
		for _, v := range g.Out(u) {
			visits++
			switch color[v] {
			case gray: // 灰=栈中，才是回边即环
				return &CycleError{Node: v}
			case white:
				if err := dfs(v); err != nil {
					return err
				}
			}
		}
		color[u] = black
		order = append(order, u)
		return nil
	}
	for u := 0; u < n; u++ {
		if color[u] == white {
			if err := dfs(u); err != nil {
				return nil, visits, err
			}
		}
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, visits, nil
}
