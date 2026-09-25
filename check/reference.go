// Package check holds the naive reference implementation used to cross-check
// the uf package, plus all tests for the module.
package check

// Reference rebuilds connectivity with BFS after every recorded edge.
type Reference struct {
	n     int
	edges [][2]int
	label []int
	count int
}

// NewReference creates n isolated elements.
func NewReference(n int) *Reference {
	r := &Reference{n: n, label: make([]int, n), count: n}
	for i := range r.label {
		r.label[i] = i
	}
	return r
}

// Union records an undirected edge between x and y, including self-loops.
func (r *Reference) Union(x, y int) {
	r.edges = append(r.edges, [2]int{x, y})
	r.rebuild()
}

// Connected reports whether BFS from x reaches y.
func (r *Reference) Connected(x, y int) bool { return r.label[x] == r.label[y] }

// Count returns the true number of connected components.
func (r *Reference) Count() int { return r.count }

func (r *Reference) rebuild() {
	adj := make([][]int, r.n)
	for _, e := range r.edges {
		a, b := e[0], e[1]
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	for i := range r.label {
		r.label[i] = -1
	}
	r.count = 0
	for seed := 0; seed < r.n; seed++ {
		if r.label[seed] != -1 {
			continue
		}
		r.label[seed] = seed
		r.count++
		queue := []int{seed}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range adj[cur] {
				if r.label[next] == -1 {
					r.label[next] = seed
					queue = append(queue, next)
				}
			}
		}
	}
}
