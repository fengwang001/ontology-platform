package ontology

// Permission filtering order (mandated by the specification):
//
//  1. Existence filter: objects the caller cannot see are removed together
//     with every link incident to them.
//  2. Traversal filter: links the caller cannot traverse are removed.
//  3. Cycle detection runs on the remaining effective subgraph.
//
// The per-caller view maintains the result of both filters incrementally, so
// detection work stays proportional to the visible subgraph rather than the
// whole graph.

// linkActiveLocked reports whether a link survives both filters for v.
// Caller must hold g.mu.
func (g *Graph) linkActiveLocked(v *callerView, l *link) bool {
	if !v.traversalGranted[l.id] {
		return false
	}
	return v.visibleObjects[l.sourceID] && v.visibleObjects[l.targetID]
}

// activateLinkLocked inserts a surviving link into the arc index. A directed
// link yields one arc (source -> target); a bidirectional link yields both
// arcs, so a pair of opposite-direction links or one bidirectional link forms
// a length-two round-trip cycle.
func (g *Graph) activateLinkLocked(v *callerView, l *link) {
	if v.activeLinks[l.id] {
		return
	}
	v.activeLinks[l.id] = true
	addArc := func(from, to string) {
		if v.outArcs[from] == nil {
			v.outArcs[from] = map[string]bool{}
		}
		v.outArcs[from][l.id] = true
	}
	addArc(l.sourceID, l.targetID)
	if l.direction == Bidirectional {
		addArc(l.targetID, l.sourceID)
	}
}

// deactivateLinkLocked removes a link from the arc index. Safe to call when
// the link is already inactive.
func (g *Graph) deactivateLinkLocked(v *callerView, linkID string) {
	if !v.activeLinks[linkID] {
		return
	}
	delete(v.activeLinks, linkID)
	for from := range v.outArcs {
		delete(v.outArcs[from], linkID)
	}
}

// refreshObjectLocked recomputes visibility of every link incident to obj.
func (g *Graph) refreshObjectLocked(v *callerView, objID string) {
	for lid := range g.incidentLinks[objID] {
		l := g.links[lid]
		active := g.linkActiveLocked(v, l)
		if active {
			g.activateLinkLocked(v, l)
		} else {
			g.deactivateLinkLocked(v, lid)
		}
	}
}

func (g *Graph) grantExistenceLocked(caller, objectID string) {
	v := g.view(caller)
	if v.existenceGranted[objectID] {
		return
	}
	v.existenceGranted[objectID] = true
	if _, exists := g.objects[objectID]; !exists {
		return
	}
	v.visibleObjects[objectID] = true
	g.refreshObjectLocked(v, objectID)
}

func (g *Graph) revokeExistenceLocked(caller, objectID string) {
	v := g.view(caller)
	if !v.existenceGranted[objectID] {
		return
	}
	delete(v.existenceGranted, objectID)
	if !v.visibleObjects[objectID] {
		return
	}
	// Existence filter first: removing the object removes all incident links
	// from the effective subgraph regardless of traversal grants.
	for lid := range g.incidentLinks[objectID] {
		g.deactivateLinkLocked(v, lid)
	}
	delete(v.visibleObjects, objectID)
	delete(v.outArcs, objectID)
}

func (g *Graph) grantTraversalLocked(caller, linkID string) {
	v := g.view(caller)
	if v.traversalGranted[linkID] {
		return
	}
	v.traversalGranted[linkID] = true
	l, ok := g.links[linkID]
	if !ok {
		return
	}
	if g.linkActiveLocked(v, l) {
		g.activateLinkLocked(v, l)
	}
}

func (g *Graph) revokeTraversalLocked(caller, linkID string) {
	v := g.view(caller)
	if !v.traversalGranted[linkID] {
		return
	}
	delete(v.traversalGranted, linkID)
	g.deactivateLinkLocked(v, linkID)
}

// GrantExistence grants the caller visibility of the given objects. Unknown
// object ids are remembered: once such an object is created it becomes
// visible without a second grant.
func (g *Graph) GrantExistence(caller string, objectIDs ...string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range objectIDs {
		if !validID(id) {
			return ErrInvalidArgument
		}
		g.grantExistenceLocked(caller, id)
	}
	return nil
}

func (g *Graph) RevokeExistence(caller string, objectIDs ...string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range objectIDs {
		if !validID(id) {
			return ErrInvalidArgument
		}
		g.revokeExistenceLocked(caller, id)
	}
	return nil
}

// GrantTraversal grants the caller traversal of the given links. Traversal
// alone never reveals an object: endpoint existence is still required.
func (g *Graph) GrantTraversal(caller string, linkIDs ...string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range linkIDs {
		if !validID(id) {
			return ErrInvalidArgument
		}
		g.grantTraversalLocked(caller, id)
	}
	return nil
}

func (g *Graph) RevokeTraversal(caller string, linkIDs ...string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range linkIDs {
		if !validID(id) {
			return ErrInvalidArgument
		}
		g.revokeTraversalLocked(caller, id)
	}
	return nil
}
