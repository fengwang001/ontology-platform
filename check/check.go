// Package check provides a naive BFS reference implementation used to verify
// the union-find structure. Connectivity is rebuilt from scratch after every
// union, so it is intentionally simple rather than performant.
package check

// Reference records an undirected edge set and answers connectivity by BFS.
type Reference struct {
	n     int
	edges [][2]int
}

// NewReference creates a reference universe of n elements.
func NewReference(n int) *Reference { return &Reference{n: n} }

// Union records an edge; it mirrors DSU by reporting whether the endpoints
// were previously in distinct components.
func (r *Reference) Union(x, y int) bool {
	merged := !r.Connected(x, y)
	if merged {
		r.edges = append(r.edges, [2]int{x, y})
	}
	return merged
}

// Connected reports connectivity by running a fresh BFS over the edge set.
func (r *Reference) Connected(x, y int) bool {
	if x < 0 || y < 0 || x >= r.n || y >= r.n {
		return false
	}
	seen := make([]bool, r.n)
	seen[x] = true
	queue := []int{x}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == y {
			return true
		}
		for _, edge := range r.edges {
			next := -1
			if edge[0] == cur {
				next = edge[1]
			} else if edge[1] == cur {
				next = edge[0]
			}
			if next >= 0 && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// Count returns the number of connected components via BFS flood fill.
func (r *Reference) Count() int {
	seen := make([]bool, r.n)
	components := 0
	for start := 0; start < r.n; start++ {
		if seen[start] {
			continue
		}
		components++
		seen[start] = true
		for queue := []int{start}; len(queue) > 0; queue = queue[1:] {
			cur := queue[0]
			for _, edge := range r.edges {
				next := -1
				if edge[0] == cur {
					next = edge[1]
				} else if edge[1] == cur {
					next = edge[0]
				}
				if next >= 0 && !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return components
}
