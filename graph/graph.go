package graph

type Edge struct {
	From   int
	To     int
	Weight float64
}

type adjacency struct {
	to     int
	weight float64
}

type Graph struct {
	order int
	edges [][]adjacency
}

func New(n int) *Graph {
	return &Graph{
		order: n,
		edges: make([][]adjacency, n),
	}
}

func (g *Graph) AddEdge(u, v int, weight float64) bool {
	if g == nil || u < 0 || u >= g.order || v < 0 || v >= g.order {
		return false
	}
	g.edges[u] = append(g.edges[u], adjacency{to: v, weight: weight})
	return true
}

func (g *Graph) Order() int {
	return g.order
}

func (g *Graph) EachNeighbor(node int, visit func(to int, weight float64) bool) {
	if g == nil || node < 0 || node >= g.order {
		return
	}
	for _, edge := range g.edges[node] {
		if !visit(edge.to, edge.weight) {
			return
		}
	}
}
