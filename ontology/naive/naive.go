// Package naive is an independent, deliberately simple reference
// implementation of the same tri-state reachability semantics. It builds
// explicit adjacency maps per decision and runs two naive fixed-point
// closures, so it is usable only on small graphs; it exists purely for
// differential testing against ontology.Graph and shares no search code.
package naive

import "ontology/ontology"

// Snapshot is one frozen state of the world for a caller.
type Snapshot struct {
	Objects     map[ontology.ID]bool
	LinkTypes   map[ontology.ID]ontology.LinkType
	Links       []ontology.LinkInstance
	Existence   map[ontology.ID]bool
	Traversable map[ontology.ID]bool
}

// Decide is the naive exhaustive decision for one query against a frozen
// snapshot. It deliberately rebuilds adjacency on every call and performs
// no incremental pruning, maximizing independence from the engine.
func Decide(snap Snapshot, from, to ontology.ID) (ontology.Outcome, string, error) {
	if err := ontology.ValidateID(from); err != nil {
		return ontology.OutcomeUnknown, "invalid_id", err
	}
	if err := ontology.ValidateID(to); err != nil {
		return ontology.OutcomeUnknown, "invalid_id", err
	}
	if !snap.Objects[from] || !snap.Objects[to] {
		return ontology.OutcomeUnknown, "endpoint_absent", ontology.ErrNotFound
	}
	if !snap.Existence[from] || !snap.Existence[to] {
		return ontology.Restricted, "endpoint_invisible", nil
	}

	type pair struct{ u, via ontology.ID }
	fwd := map[ontology.ID][]pair{}
	rev := map[ontology.ID][]pair{}
	for _, l := range snap.Links {
		lt, ok := snap.LinkTypes[l.LinkType]
		if !ok {
			continue
		}
		fwd[l.Tail] = append(fwd[l.Tail], pair{l.Head, l.LinkType})
		rev[l.Head] = append(rev[l.Head], pair{l.Tail, l.LinkType})
		if lt.Direction == ontology.Bidirectional {
			fwd[l.Head] = append(fwd[l.Head], pair{l.Tail, l.LinkType})
			rev[l.Tail] = append(rev[l.Tail], pair{l.Head, l.LinkType})
		}
	}

	if from == to {
		return ontology.Reachable, "same_visible_object", nil
	}

	// Closure 1: everything reachable using only permitted link types.
	allowed := map[ontology.ID]bool{from: true}
	frontier := []ontology.ID{from}
	denied := map[ontology.ID]bool{}
	for len(frontier) > 0 {
		u := frontier[0]
		frontier = frontier[1:]
		for _, p := range fwd[u] {
			if !snap.Traversable[p.via] {
				denied[p.u] = true
				continue
			}
			if !allowed[p.u] {
				allowed[p.u] = true
				frontier = append(frontier, p.u)
			}
		}
	}
	if allowed[to] {
		return ontology.Reachable, "fully_traversable_path_found", nil
	}
	if len(denied) == 0 {
		return ontology.Unreachable, "exhausted_search_no_candidate", nil
	}

	// Closure 2 (permission-free): everything that can flow into target.
	back := map[ontology.ID]bool{to: true}
	frontier = []ontology.ID{to}
	for len(frontier) > 0 {
		v := frontier[0]
		frontier = frontier[1:]
		if denied[v] {
			return ontology.Restricted, "denied_arc_may_reach_target", nil
		}
		for _, p := range rev[v] {
			if !back[p.u] {
				back[p.u] = true
				frontier = append(frontier, p.u)
			}
		}
	}
	return ontology.Unreachable, "denied_arcs_cannot_reach_target", nil
}
