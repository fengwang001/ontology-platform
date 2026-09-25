package check

import "ontology/uf"

type Reference struct {
	n         int
	adj       [][]int
	component []int
	count     int
}

func NewReference(n int) *Reference {
	r := &Reference{
		n:         n,
		adj:       make([][]int, n),
		component: make([]int, n),
	}
	r.rebuild()
	return r
}

func (r *Reference) Union(x, y int) (bool, error) {
	if x < 0 || x >= r.n || y < 0 || y >= r.n {
		return false, uf.ErrBadIndex
	}
	merged := r.component[x] != r.component[y]
	if merged {
		r.adj[x] = append(r.adj[x], y)
		r.adj[y] = append(r.adj[y], x)
	}
	r.rebuild()
	return merged, nil
}

func (r *Reference) Connected(x, y int) (bool, error) {
	if x < 0 || x >= r.n || y < 0 || y >= r.n {
		return false, uf.ErrBadIndex
	}
	return r.component[x] == r.component[y], nil
}

func (r *Reference) Count() int {
	return r.count
}

func (r *Reference) rebuild() {
	for i := range r.component {
		r.component[i] = -1
	}
	r.count = 0
	for start := 0; start < r.n; start++ {
		if r.component[start] != -1 {
			continue
		}
		queue := []int{start}
		r.component[start] = r.count
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for _, next := range r.adj[x] {
				if r.component[next] == -1 {
					r.component[next] = r.count
					queue = append(queue, next)
				}
			}
		}
		r.count++
	}
}
