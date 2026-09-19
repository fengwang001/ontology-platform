package ontology

import (
	"sort"
	"strings"
)

// FindCycles returns every elementary cycle reachable from start when
// traversing the given link types in BOTH directions (links are traversable
// either way). Each cycle is a list of object ids in traversal order,
// canonicalised to start at its lexicographically smallest member with the
// smaller of the two possible directions. The result is deduplicated and
// sorted, so it is stable regardless of map iteration order. A self-link
// yields a single-element cycle.
func (s *Store) FindCycles(start string, linkTypes []string) [][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	adj := buildAdjacency(s.st, linkTypes)
	if len(adj[start]) == 0 {
		return nil
	}
	reachable := reachableNodes(adj, start)
	seen := make(map[string]bool)
	var cycles [][]string
	for _, root := range reachable {
		if adj[root][root] {
			key := root
			if !seen[key] {
				seen[key] = true
				cycles = append(cycles, []string{root})
			}
		}
		// Enumerate cycles whose smallest member is root by only visiting
		// nodes strictly greater than root.
		visited := map[string]bool{root: true}
		walkCycles(adj, root, root, []string{root}, visited, seen, &cycles)
	}
	sort.Slice(cycles, func(i, j int) bool {
		return cycleKey(cycles[i]) < cycleKey(cycles[j])
	})
	return cycles
}

func buildAdjacency(st *state, linkTypes []string) map[string]map[string]bool {
	adj := make(map[string]map[string]bool)
	add := func(a, b string) {
		if adj[a] == nil {
			adj[a] = make(map[string]bool)
		}
		adj[a][b] = true
	}
	for _, lt := range linkTypes {
		for src, targets := range st.fwd[lt] {
			for dst := range targets {
				add(src, dst)
				add(dst, src)
			}
		}
	}
	return adj
}

func reachableNodes(adj map[string]map[string]bool, start string) []string {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for nb := range adj[cur] {
			if !seen[nb] {
				seen[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func walkCycles(adj map[string]map[string]bool, root, cur string,
	path []string, visited map[string]bool, seen map[string]bool, cycles *[][]string) {
	for _, nb := range sortedKeys(adj[cur]) {
		if nb == root {
			if len(path) >= 3 { // 2-node back-and-forth is not a cycle
				recordCycle(path, seen, cycles)
			}
			continue
		}
		if nb <= root || visited[nb] {
			continue
		}
		visited[nb] = true
		walkCycles(adj, root, nb, append(path, nb), visited, seen, cycles)
		delete(visited, nb)
	}
}

// recordCycle canonicalises direction (a->b->c vs a->c->b are the same
// undirected cycle) and dedupes.
func recordCycle(path []string, seen map[string]bool, cycles *[][]string) {
	rev := make([]string, len(path))
	rev[0] = path[0]
	for i := 1; i < len(path); i++ {
		rev[i] = path[len(path)-i]
	}
	best := path
	if cycleKey(rev) < cycleKey(path) {
		best = rev
	}
	key := cycleKey(best)
	if seen[key] {
		return
	}
	seen[key] = true
	cp := make([]string, len(best))
	copy(cp, best)
	*cycles = append(*cycles, cp)
}

func cycleKey(c []string) string {
	return strings.Join(c, "\x00")
}
