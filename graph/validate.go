package graph

import "fmt"

// Validate checks that the graph is acyclic using Kahn's algorithm. On a
// cycle it derives a real closed path from the remaining nodes via DFS.
func (g *Graph) Validate() error {
	indeg := make(map[string]int, len(g.nodes))
	for _, id := range g.nodes {
		indeg[id] = len(g.pred[id])
	}
	queue := make([]string, 0, len(g.nodes))
	for _, id := range g.nodes {
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		visited++
		for _, next := range g.succ[id] {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited == len(g.nodes) {
		return nil
	}
	remaining := map[string]bool{}
	for id, d := range indeg {
		if d > 0 {
			remaining[id] = true
		}
	}
	return &CycleError{Path: g.findCycle(remaining)}
}

// findCycle returns a closed walk within the cyclic remainder. Every node in
// the remainder has at least one predecessor inside it, so following any
// predecessor must eventually revisit a node.
func (g *Graph) findCycle(remaining map[string]bool) []string {
	var start string
	for id := range remaining {
		start = id
		break
	}
	index := map[string]int{}
	var path []string
	cur := start
	for {
		if pos, ok := index[cur]; ok {
			// We walked predecessor links, so edges point from the next
			// node to the previous one; reverse the segment to report the
			// cycle in edge direction.
			seg := append([]string(nil), path[pos:]...)
			reverse(seg)
			return append(seg, seg[0])
		}
		index[cur] = len(path)
		path = append(path, cur)
		for _, p := range g.pred[cur] {
			if remaining[p] {
				cur = p
				break
			}
		}
	}
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Layers returns the topological layers of the DAG. Layer i contains every
// node whose longest dependency chain has length i; all nodes inside one
// layer are independent and may run concurrently.
func (g *Graph) Layers() ([][]string, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	level := make(map[string]int, len(g.nodes))
	indeg := make(map[string]int, len(g.nodes))
	queue := make([]string, 0, len(g.nodes))
	for _, id := range g.nodes {
		indeg[id] = len(g.pred[id])
		if indeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	maxLevel := 0
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		for _, next := range g.succ[id] {
			if level[id]+1 > level[next] {
				level[next] = level[id] + 1
			}
			if level[next] > maxLevel {
				maxLevel = level[next]
			}
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	layers := make([][]string, maxLevel+1)
	for _, id := range g.nodes {
		layers[level[id]] = append(layers[level[id]], id)
	}
	return layers, nil
}

// CheckCyclePath verifies that path is a real closed walk of existing edges,
// mainly useful in tests.
func (g *Graph) CheckCyclePath(path []string) error {
	if len(path) < 2 || path[0] != path[len(path)-1] {
		return fmt.Errorf("graph: path is not closed")
	}
	for i := 0; i+1 < len(path); i++ {
		if !g.Has(path[i]) || !g.Has(path[i+1]) {
			return fmt.Errorf("graph: unknown node in cycle path")
		}
		if !g.hasEdge(path[i], path[i+1]) {
			return fmt.Errorf("graph: missing edge %s->%s", path[i], path[i+1])
		}
	}
	return nil
}

func (g *Graph) hasEdge(a, b string) bool {
	for _, s := range g.succ[a] {
		if s == b {
			return true
		}
	}
	return false
}
