package graph

// Layers returns the topological levels of the DAG: layer 0 contains steps
// with no prerequisites, layer k contains steps whose prerequisites are all in
// layers < k. Steps inside one layer are independent and may run concurrently.
// The graph is assumed validated; a graph containing a cycle yields layers
// that do not cover every node.
func (g *Graph) Layers() [][]string {
	remaining := make(map[string]int, len(g.ids))
	var frontier []string
	for _, id := range g.ids {
		remaining[id] = g.indeg[id]
		if g.indeg[id] == 0 {
			frontier = append(frontier, id)
		}
	}

	var layers [][]string
	seen := 0
	for len(frontier) > 0 {
		layer := append([]string(nil), frontier...)
		layers = append(layers, layer)
		seen += len(layer)
		var next []string
		for _, node := range layer {
			for _, child := range g.succ[node] {
				remaining[child]--
				if remaining[child] == 0 {
					next = append(next, child)
				}
			}
		}
		frontier = next
	}
	if seen < len(g.ids) {
		// Should be unreachable for a graph returned by Build.
		panic("graph: layers computed on cyclic graph")
	}
	return layers
}

// TopoOrder returns a single deterministic topological ordering (scheduling
// direction).
func (g *Graph) TopoOrder() []string {
	layers := g.Layers()
	var order []string
	for _, layer := range layers {
		order = append(order, layer...)
	}
	return order
}

// ReverseTopoOrder returns a topological ordering in compensation direction:
// every node appears after all nodes that depended on it. With the chain
// A->B->C the result is [C B A], so compensating C, then B, then A respects
// "undo the dependent before the dependency".
func (g *Graph) ReverseTopoOrder() []string {
	order := g.TopoOrder()
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// LayerOf returns the layer index of each step.
func (g *Graph) LayerOf() map[string]int {
	index := make(map[string]int, len(g.ids))
	for i, layer := range g.Layers() {
		for _, id := range layer {
			index[id] = i
		}
	}
	return index
}

// Size returns the number of registered steps.
func (g *Graph) Size() int { return len(g.ids) }
