// Package check is the naive Kahn's-algorithm reference for topo.TopoSort.
package check

// Kahn returns a topological order of nodes 0..n-1, or ok=false on a cycle.
// It assumes all edge endpoints are in range.
func Kahn(n int, edges [][2]int) (order []int, ok bool) {
	indeg := make([]int, n)
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		indeg[e[1]]++
	}
	var queue []int
	for v := 0; v < n; v++ {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return order, len(order) == n
}
