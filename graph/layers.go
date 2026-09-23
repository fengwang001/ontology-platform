package graph

import "sort"

// Layers returns the topological levels of the graph via an iterative Kahn
// process (no recursion, so long chains cannot overflow the stack). Each
// layer holds nodes whose predecessors are all in earlier layers; nodes
// inside a layer are sorted by id. Returns nil if a cycle exists.
func (g *Graph) Layers() [][]string {
	indeg := make(map[string]int, len(g.order))
	for _, id := range g.order {
		indeg[id] = len(g.pred[id])
	}

	var ready []string
	for _, id := range g.order {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	seen := 0
	var layers [][]string
	for len(ready) > 0 {
		layer := append([]string{}, ready...)
		layers = append(layers, layer)
		seen += len(layer)

		var next []string
		for _, u := range layer {
			for _, v := range g.Successors(u) {
				indeg[v]--
				if indeg[v] == 0 {
					next = append(next, v)
				}
			}
		}
		sort.Strings(next)
		ready = next
	}
	if seen != len(g.order) {
		return nil
	}
	return layers
}

// Topo returns a single topological ordering (ids sorted within each level),
// or nil on a cycle.
func (g *Graph) Topo() []string {
	layers := g.Layers()
	if layers == nil {
		return nil
	}
	var out []string
	for _, l := range layers {
		out = append(out, l...)
	}
	return out
}
