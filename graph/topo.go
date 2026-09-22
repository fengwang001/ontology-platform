package graph

// Layers groups nodes into topological levels using Kahn's algorithm:
// layer 0 holds nodes with no dependencies, layer k holds nodes whose
// dependencies all live in layers < k. Nodes inside one layer are
// independent and may run concurrently. Validate must be called first;
// the graph is acyclic then, so every node lands in exactly one layer.
//
// The result is deterministic: nodes are processed in insertion order.
func (g *Graph) Layers() [][]string {
	indeg := make(map[string]int, len(g.order))
	for _, id := range g.order {
		indeg[id] = g.indeg[id]
	}
	var layers [][]string
	frontier := g.zeroIndegree(indeg)
	for len(frontier) > 0 {
		layer := frontier
		layers = append(layers, layer)
		for _, id := range layer {
			for _, to := range g.succ[id] {
				indeg[to]--
			}
		}
		frontier = g.zeroIndegree(indeg)
	}
	return layers
}

// zeroIndegree collects nodes whose indegree just dropped to zero, in
// insertion order, and marks them consumed (-1) so they are never
// collected twice.
func (g *Graph) zeroIndegree(indeg map[string]int) []string {
	var out []string
	for _, id := range g.order {
		if indeg[id] == 0 {
			out = append(out, id)
			indeg[id] = -1
		}
	}
	return out
}

// TopoOrder flattens Layers into a single topological ordering.
func (g *Graph) TopoOrder() []string {
	var out []string
	for _, layer := range g.Layers() {
		out = append(out, layer...)
	}
	return out
}

// ReverseTopoOrder returns a topological ordering reversed: every node
// appears before all of its dependencies' successors, i.e. dependents
// come before the steps they depended on.
func (g *Graph) ReverseTopoOrder() []string {
	topo := g.TopoOrder()
	out := make([]string, len(topo))
	for i, id := range topo {
		out[len(topo)-1-i] = id
	}
	return out
}
