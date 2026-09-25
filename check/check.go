// Package check provides a naive reference model: connectivity is rebuilt
// by BFS after every union. It exists only to validate package uf.
package check

// Naive is an adjacency-list reference with no balancing or compression.
type Naive struct {
	n     int
	edges [][2]int
}

func NewNaive(n int) *Naive { return &Naive{n: n} }

// Union records an edge; it returns false for out-of-range endpoints.
func (m *Naive) Union(x, y int) bool {
	if x < 0 || x >= m.n || y < 0 || y >= m.n {
		return false
	}
	m.edges = append(m.edges, [2]int{x, y})
	return true
}

func (m *Naive) adjacency() [][]int {
	adj := make([][]int, m.n)
	for _, e := range m.edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		adj[e[1]] = append(adj[e[1]], e[0])
	}
	return adj
}

func (m *Naive) Connected(x, y int) bool {
	if x < 0 || x >= m.n || y < 0 || y >= m.n {
		return false
	}
	adj := m.adjacency()
	seen := make([]bool, m.n)
	queue := []int{x}
	seen[x] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == y {
			return true
		}
		for _, next := range adj[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// Components counts connected components by BFS over the whole graph.
func (m *Naive) Components() int {
	adj := m.adjacency()
	seen := make([]bool, m.n)
	count := 0
	for start := 0; start < m.n; start++ {
		if seen[start] {
			continue
		}
		count++
		seen[start] = true
		queue := []int{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur] {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return count
}

// BadFind is the deliberately broken implementation: roots are always
// attached under x's root and no path compression is performed.
type BadFind struct{ parent []int }

func NewBadFind(n int) *BadFind {
	b := &BadFind{parent: make([]int, n)}
	for i := range b.parent {
		b.parent[i] = i
	}
	return b
}

// Find walks parent links without compression and returns the hop count.
func (b *BadFind) Find(x int) (int, int) {
	hops := 0
	for b.parent[x] != x {
		x = b.parent[x]
		hops++
	}
	return x, hops
}

// Union always attaches y's root beneath x's root.
func (b *BadFind) Union(x, y int) {
	rx, _ := b.Find(x)
	ry, _ := b.Find(y)
	if rx != ry {
		b.parent[ry] = rx
	}
}
