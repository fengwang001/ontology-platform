package ontology

import "sort"

// NaiveTraverse is an independent reference implementation that maintains
// the full ancestor sequence per path and scans it linearly.
// It intentionally uses no hash sets for ancestor membership: every cycle
// check scans the complete current-path ancestor slice (O(path length) per
// hop). It is used solely for cross-checking the production traversal on
// random graphs.
func NaiveTraverse(snap Snapshot, req TraverseRequest) (TraverseResult, error) {
	if !snap.HasObject(req.Start) {
		return TraverseResult{}, ErrStartObjectNotFound
	}
	if err := req.validate(snap); err != nil {
		return TraverseResult{}, err
	}

	n := &naiveTraversal{snap: snap, req: req}
	n.dfs([]ObjectID{req.Start}, nil)

	sort.SliceStable(n.result.Paths, func(i, j int) bool {
		return comparePath(n.result.Paths[i], n.result.Paths[j]) < 0
	})
	return n.result, nil
}

type naiveTraversal struct {
	snap   Snapshot
	req    TraverseRequest
	result TraverseResult
}

func (n *naiveTraversal) dfs(nodeSeq []ObjectID, linkSeq []LinkID) {
	current := nodeSeq[len(nodeSeq)-1]
	refs := n.neighbors(current)
	if len(refs) == 0 {
		n.emit(nodeSeq, linkSeq, TerminatedBoundary)
		return
	}

	depth := len(nodeSeq) - 1
	for _, ref := range refs {
		// The naive probe: linear scan over the entire ancestor sequence.
		n.result.AncestorChecks++
		isCycle := false
		for _, anc := range nodeSeq {
			if anc == ref.Target {
				isCycle = true
				break
			}
		}

		ancestorsAtDecision := append([]ObjectID(nil), nodeSeq...)
		nodes := append(append([]ObjectID(nil), nodeSeq...), ref.Target)
		links := append(append([]LinkID(nil), linkSeq...), ref.LinkID)

		// Same fixed priority: cycle before depth truncation.
		if isCycle {
			n.emitWith(nodes, links, TerminatedCycle, ancestorsAtDecision)
			continue
		}
		if depth+1 >= n.req.MaxDepth {
			n.emitWith(nodes, links, TerminatedDepthLimit, ancestorsAtDecision)
			continue
		}
		n.dfs(nodes, links)
	}
}

func (n *naiveTraversal) neighbors(current ObjectID) []LinkRef {
	keys := make([]LinkType, 0, len(n.req.LinkTypes))
	for k := range n.req.LinkTypes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var refs []LinkRef
	for _, k := range keys {
		refs = append(refs, n.snap.Neighbors(current, k, n.req.LinkTypes[k])...)
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].LinkID < refs[j].LinkID })
	return refs
}

func (n *naiveTraversal) emit(nodeSeq []ObjectID, linkSeq []LinkID, reason TerminalReason) {
	n.emitWith(
		append([]ObjectID(nil), nodeSeq...),
		append([]LinkID(nil), linkSeq...),
		reason,
		append([]ObjectID(nil), nodeSeq...),
	)
}

func (n *naiveTraversal) emitWith(nodes []ObjectID, links []LinkID, reason TerminalReason, ancestors []ObjectID) {
	n.result.Paths = append(n.result.Paths, Path{
		Nodes:     nodes,
		Links:     links,
		Reason:    reason,
		Ancestors: ancestors,
	})
}
