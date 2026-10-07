package ontology

// snapshot is an immutable, self-contained copy of one caller's effective
// subgraph at one point in the serial order. Building it touches only visible
// objects and active links.
type snapshot struct {
	// nodes is the existence-filtered object set.
	nodes map[string]struct{}
	// arcs[u] lists active link ids leaving u in a fixed sorted order.
	arcs map[string][]arc
	// linkCount is the number of distinct active links (parallel links count
	// separately, but never twice in the same arc slot).
	linkCount int
	// nodesExamined / linksExamined are the internal cost metric: exactly the
	// number of visible objects and active links touched while producing the
	// snapshot. Invisible data is never traversed.
	nodesExamined int
	linksExamined int
}

type arc struct {
	linkID string
	to     string
}

// buildSnapshotLocked copies the effective subgraph. Caller must hold g.mu
// (at least read-locked in practice).
func (g *Graph) buildSnapshotLocked(v *callerView) snapshot {
	snap := snapshot{
		nodes: make(map[string]struct{}, len(v.visibleObjects)),
		arcs:  make(map[string][]arc, len(v.visibleObjects)),
	}
	for id := range v.visibleObjects {
		snap.nodes[id] = struct{}{}
	}
	snap.nodesExamined = len(snap.nodes)
	for linkID := range v.activeLinks {
		l := g.links[linkID]
		if l == nil {
			continue
		}
		snap.appendArc(l.sourceID, l.targetID, linkID)
		if l.direction == Bidirectional {
			snap.appendArc(l.targetID, l.sourceID, linkID)
		}
		snap.linkCount++
		snap.linksExamined++
	}
	for u := range snap.arcs {
		sortArcs(snap.arcs[u])
	}
	return snap
}

func (s *snapshot) appendArc(from, to, linkID string) {
	_, okFrom := s.nodes[from]
	_, okTo := s.nodes[to]
	if !okFrom || !okTo {
		return
	}
	s.arcs[from] = append(s.arcs[from], arc{linkID: linkID, to: to})
}

func sortArcs(a []arc) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && (a[j-1].to > a[j].to ||
			(a[j-1].to == a[j].to && a[j-1].linkID > a[j].linkID)); j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
