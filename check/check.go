// Package check 提供朴素参照实现（Kahn 入度法）用于对照验证 topo。
package check

import (
	"fmt"

	"ontology/topo"
)

// Kahn 入度法拓扑排序；就绪节点按编号升序出队以保证确定性。
// 有环返回 topo.ErrCycle，端点越界返回 topo.ErrBadEdge。
func Kahn(n int, edges []topo.Edge) ([]int, error) {
	if n < 0 {
		return nil, fmt.Errorf("%w: n=%d", topo.ErrBadEdge, n)
	}
	indeg := make([]int, n)
	adj := make([][]int, n)
	for _, e := range edges {
		if e.U < 0 || e.U >= n || e.V < 0 || e.V >= n {
			return nil, fmt.Errorf("%w: %d->%d (n=%d)", topo.ErrBadEdge, e.U, e.V, n)
		}
		adj[e.U] = append(adj[e.U], e.V)
		indeg[e.V]++
	}
	var ready []int
	for u := 0; u < n; u++ {
		if indeg[u] == 0 {
			ready = append(ready, u)
		}
	}
	var order []int
	for len(ready) > 0 {
		u := ready[0]
		ready = ready[1:]
		order = append(order, u)
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				ready = append(ready, v)
			}
		}
	}
	if len(order) != n {
		return nil, topo.ErrCycle
	}
	return order, nil
}

// validOrder 校验 order 是 0..n-1 的排列且满足每条边 u→v 中 u 在 v 之前。
func validOrder(n int, edges []topo.Edge, order []int) bool {
	pos, seen := make([]int, n), make([]bool, n)
	for i, v := range order {
		if v < 0 || v >= n || seen[v] {
			return false
		}
		seen[v], pos[v] = true, i
	}
	for _, e := range edges {
		if pos[e.U] >= pos[e.V] {
			return false
		}
	}
	return len(order) == n
}
