package reachability

import "sort"

// bfsPathLocked returns a shortest walk from -> to in the current succ graph
// as the ordered list of nodes including both endpoints. It returns nil when
// to is not reachable by a walk of at least one edge, so from == to requires a
// directed cycle. Neighbours are expanded in sorted order, which makes the
// returned path deterministic. The caller must hold r.mu.
func (r *Reachability) bfsPathLocked(from, to string) []string {
	pred := map[string]string{}
	visited := map[string]struct{}{from: {}}
	queue := []string{from}

	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]

		neighbours := make([]string, 0, len(r.succ[u]))
		for v := range r.succ[u] {
			neighbours = append(neighbours, v)
		}
		sort.Strings(neighbours)

		for _, v := range neighbours {
			if v == from {
				// An edge back to the root is the positive-length witness for
				// the root reaching itself. It can be a self loop (u == from)
				// or the edge closing a longer directed cycle.
				if to != from {
					continue
				}
				return cyclePath(pred, from, u)
			}
			if _, seen := visited[v]; seen {
				continue
			}
			visited[v] = struct{}{}
			pred[v] = u
			if v == to {
				return reconstructPath(pred, from, to)
			}
			queue = append(queue, v)
		}
	}
	return nil
}

// reconstructPath walks predecessor links from to back to from and returns
// the reversed chain [from, ..., to].
func reconstructPath(pred map[string]string, from, to string) []string {
	chain := []string{to}
	for cur := to; cur != from; {
		p := pred[cur]
		chain = append(chain, p)
		cur = p
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// cyclePath returns a positive-length walk from the root back to itself:
// [from, ..., via, from], where via is the node whose edge closes the cycle.
func cyclePath(pred map[string]string, from, via string) []string {
	chain := []string{via}
	for cur := via; cur != from; {
		p := pred[cur]
		chain = append(chain, p)
		cur = p
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return append(chain, from)
}

// naivePairs computes all reachable pairs of a multigraph from its edge
// multiplicities with a plain per-source breadth-first traversal. Edges with
// multiplicity zero are absent. This is the simple reference implementation
// used to cross-check the incremental maintenance; it sorts the result
// lexicographically.
func naivePairs(edges map[[2]string]int) [][2]string {
	succ := make(map[string]map[string]struct{})
	nodes := make(map[string]struct{})
	for e, m := range edges {
		if m <= 0 {
			continue
		}
		outs := succ[e[0]]
		if outs == nil {
			outs = make(map[string]struct{})
			succ[e[0]] = outs
		}
		outs[e[1]] = struct{}{}
		nodes[e[0]] = struct{}{}
		nodes[e[1]] = struct{}{}
	}

	sources := make([]string, 0, len(nodes))
	for n := range nodes {
		sources = append(sources, n)
	}
	sort.Strings(sources)

	pairs := make([][2]string, 0)
	for _, src := range sources {
		visited := map[string]struct{}{src: {}}
		selfEmitted := false
		queue := []string{src}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for v := range succ[u] {
				if v == src {
					if !selfEmitted {
						selfEmitted = true
						pairs = append(pairs, [2]string{src, src})
					}
					continue
				}
				if _, seen := visited[v]; seen {
					continue
				}
				visited[v] = struct{}{}
				pairs = append(pairs, [2]string{src, v})
				queue = append(queue, v)
			}
		}
	}
	sortPairs(pairs)
	return pairs
}
