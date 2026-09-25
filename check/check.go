// Package check provides a naive reference that rebuilds connectivity
// by BFS after every Union, plus helpers shared by the tests.
package check

import "ontology/uf"

// Naive tracks equivalence edges and answers queries by BFS.
type Naive struct {
	n     int
	edges [][2]int
}

// NewNaive returns a reference structure for n elements.
func NewNaive(n int) *Naive { return &Naive{n: n} }

// Union records an equivalence edge between x and y.
func (v *Naive) Union(x, y int) { v.edges = append(v.edges, [2]int{x, y}) }

// component returns the set of nodes reachable from src via BFS.
func (v *Naive) component(src int) map[int]bool {
	seen := map[int]bool{src: true}
	queue := []int{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range v.edges {
			next, ok := e[0], false
			if e[0] == cur {
				next, ok = e[1], true
			} else if e[1] == cur {
				next, ok = e[0], true
			}
			if ok && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// Connected reports whether x and y are in the same component.
func (v *Naive) Connected(x, y int) bool { return v.component(x)[y] }

// Count returns the number of connected components.
func (v *Naive) Count() int {
	unseen := make(map[int]bool, v.n)
	for i := 0; i < v.n; i++ {
		unseen[i] = true
	}
	count := 0
	for i := 0; i < v.n; i++ {
		if unseen[i] {
			count++
			for node := range v.component(i) {
				delete(unseen, node)
			}
		}
	}
	return count
}

// run applies ops to a fresh u and a Naive reference, reporting whether
// Union merge results and final connectivity agree. u must be fresh.
func run(u *uf.UF, ops [][2]int) bool {
	ref := NewNaive(u.Count())
	for _, op := range ops {
		merged, err := u.Union(op[0], op[1])
		if err != nil || merged == ref.Connected(op[0], op[1]) {
			return false
		}
		ref.Union(op[0], op[1])
	}
	for x := 0; x < ref.n; x++ {
		for y := 0; y < ref.n; y++ {
			if got, _ := u.Connected(x, y); got != ref.Connected(x, y) {
				return false
			}
		}
	}
	return u.Count() == ref.Count()
}
