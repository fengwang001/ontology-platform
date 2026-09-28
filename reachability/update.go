package reachability

// addPairLocked inserts the reachable pair (u, v).
// The caller must hold r.mu for writing.
func (r *Reachability) addPairLocked(u, v string) {
	outs := r.reach[u]
	if outs == nil {
		outs = make(map[string]struct{})
		r.reach[u] = outs
	}
	outs[v] = struct{}{}
}

// addEdgeLocked applies the closure update for adding the new edge from -> to.
// The edge is already present in r.succ but not yet reflected in r.reach.
//
// Every walk that uses the new edge consists of: an old walk from its source
// to "from", the new edge itself, and an old walk from "to" to its target.
// Hence the newly reachable pairs are exactly S x T, where S is "from" plus
// its ancestors in the old closure and T is "to" plus its old descendants.
// The caller must hold r.mu for writing.
func (r *Reachability) addEdgeLocked(from, to string) {
	sources := r.ancestorsLocked(from)
	targets := r.descendantsLocked(to)

	for s := range sources {
		for t := range targets {
			r.addPairLocked(s, t)
		}
	}
}

// removeEdgeLocked applies the closure update for deleting the edge from -> to,
// after it has been removed from r.succ but while r.reach still describes the
// graph containing the edge.
//
// Strategy:
//  1. Build S = "from" plus everything that could reach it, and T = "to" plus
//     everything reachable from it, in the old closure. Every pair whose old
//     witnessing walk could use the deleted edge lies in S x T, so remove the
//     whole rectangle first.
//  2. Re-run a forward traversal on the remaining graph from every source in
//     S and add back exactly the pairs of S x T that are still reachable.
//
// Pairs outside S x T could never have used the edge and are left untouched.
// The caller must hold r.mu for writing.
func (r *Reachability) removeEdgeLocked(from, to string) {
	sources := r.ancestorsLocked(from)
	targets := r.descendantsLocked(to)

	// Step 1: remove every possibly affected pair.
	for s := range sources {
		outs := r.reach[s]
		if outs == nil {
			continue
		}
		for t := range targets {
			delete(outs, t)
		}
		if len(outs) == 0 {
			delete(r.reach, s)
		}
	}

	// Step 2: re-derive on the remaining graph.
	for s := range sources {
		r.deriveLocked(s, targets)
	}
}

// ancestorsLocked returns {node} together with every node having a path of at
// least one edge to node in the current closure.
func (r *Reachability) ancestorsLocked(node string) map[string]struct{} {
	set := map[string]struct{}{node: {}}
	for u, outs := range r.reach {
		if _, ok := outs[node]; ok {
			set[u] = struct{}{}
		}
	}
	return set
}

// descendantsLocked returns {node} together with every node reachable from
// node by a path of at least one edge in the current closure.
func (r *Reachability) descendantsLocked(node string) map[string]struct{} {
	set := map[string]struct{}{node: {}}
	for v := range r.reach[node] {
		set[v] = struct{}{}
	}
	return set
}

// deriveLocked performs a forward BFS from src over the current (post-delete)
// adjacency and re-adds pairs (src, x) for targets x that are reachable by a
// walk of at least one edge. The root itself is only added back when it lies
// on a remaining directed cycle.
//
// The caller must hold r.mu for writing.
func (r *Reachability) deriveLocked(src string, targets map[string]struct{}) {
	visited := map[string]struct{}{src: {}}
	queue := []string{src}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := range r.succ[u] {
			// The root is "visited" from the start; a later edge back into it
			// is the positive-length walk that makes (src, src) reachable.
			if v == src {
				if _, ok := targets[src]; ok {
					r.addPairLocked(src, src)
				}
				continue
			}
			if _, seen := visited[v]; seen {
				continue
			}
			visited[v] = struct{}{}
			queue = append(queue, v)
			if _, ok := targets[v]; ok {
				r.addPairLocked(src, v)
			}
		}
	}
}
