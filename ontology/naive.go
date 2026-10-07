package ontology

import "context"

// NaiveClassify is an independently written, deliberately simple ground
// truth reference: it ignores all permissions and answers only whether a
// path exists in the full graph.
//
// It deliberately does not share any code with reachableFrom: it rebuilds
// the adjacency relation from raw link instances and runs a textbook
// recursive DFS with an on-stack guard. Missing endpoints and malformed ids
// are reported the same way as the production API so the differential test
// can compare both layers.
func NaiveClassify(ctx context.Context, g *Graph, start, end string) (bool, error) {
	if !validIdentifier(start) {
		return false, ErrInvalidID(start)
	}
	if !validIdentifier(end) {
		return false, ErrInvalidID(end)
	}
	s := g.snapshot()
	if _, ok := s.objects[start]; !ok {
		return false, ErrMissingStart(start)
	}
	if _, ok := s.objects[end]; !ok {
		return false, ErrMissingEnd(end)
	}

	type arc struct{ via, to string }
	adj := map[string][]arc{}
	for _, l := range s.links {
		dir := s.linkTypes[l.LinkType]
		adj[l.Src] = append(adj[l.Src], arc{l.LinkType, l.Dst})
		if dir == Bidirectional {
			adj[l.Dst] = append(adj[l.Dst], arc{l.LinkType, l.Src})
		}
	}

	visited := map[string]bool{}
	var dfs func(string) bool
	dfs = func(node string) bool {
		if node == end {
			return true
		}
		if err := ctx.Err(); err != nil {
			return false
		}
		visited[node] = true
		for _, a := range adj[node] {
			if !visited[a.to] && dfs(a.to) {
				return true
			}
		}
		return false
	}
	return dfs(start), nil
}
