package initsession

import "sort"

// sccResult is a Tarjan decomposition of a directed graph.
type sccResult struct {
	// sccOf[v] is the component id of vertex v.
	sccOf []int
	// count is the number of components.
	count int
	// order lists components in reverse topological order: for every
	// edge u->v with sccOf[u] != sccOf[v], the component of v appears
	// before the component of u. Each entry lists member vertices in
	// ascending order.
	order [][]int
}

// tarjan computes strongly connected components of adj with an
// explicit stack (O(V+E) time, no recursion).
func tarjan(adj [][]int) sccResult {
	n := len(adj)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	next := 0
	comp := make([]int, n)
	compCount := 0
	var compOrder [][]int

	type frame struct {
		v, pi int
	}

	for root := 0; root < n; root++ {
		if index[root] != -1 {
			continue
		}
		index[root] = next
		low[root] = next
		next++
		stack = append(stack, root)
		onStack[root] = true
		work := []frame{{v: root, pi: 0}}

		for len(work) > 0 {
			top := &work[len(work)-1]
			v := top.v
			if top.pi < len(adj[v]) {
				w := adj[v][top.pi]
				top.pi++
				if index[w] == -1 {
					index[w] = next
					low[w] = next
					next++
					stack = append(stack, w)
					onStack[w] = true
					work = append(work, frame{v: w, pi: 0})
					continue
				}
				if onStack[w] && index[w] < low[v] {
					low[v] = index[w]
				}
				continue
			}

			if low[v] == index[v] {
				var members []int
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					comp[w] = compCount
					members = append(members, w)
					if w == v {
						break
					}
				}
				sort.Ints(members)
				compOrder = append(compOrder, members)
				compCount++
			}
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1].v
				if low[v] < low[parent] {
					low[parent] = low[v]
				}
			}
		}
	}

	return sccResult{sccOf: comp, count: compCount, order: compOrder}

}

func sortInt32s(x []int32) {
	sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
}

func sortInts(x []int) {
	sort.Ints(x)
}
