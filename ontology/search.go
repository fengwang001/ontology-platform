package ontology

import "container/heap"

// nodeState is the best-known route to one object during the search.
type nodeState struct {
	dist  float64
	path  []ObjectID
	links []LinkType
}

// pqItem is one heap entry.
type pqItem struct {
	id   ObjectID
	dist float64
	path []ObjectID
	idx  int
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].dist != pq[j].dist {
		return pq[i].dist < pq[j].dist
	}
	return lexLess(pq[i].path, pq[j].path)
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].idx = i
	pq[j].idx = j
}

func (pq *priorityQueue) Push(x any) {
	item := x.(*pqItem)
	item.idx = len(*pq)
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*pq = old[:n-1]
	return item
}

// lexLess compares two object-id sequences lexicographically.
func lexLess(a, b []ObjectID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// appendCopy returns a new slice with x appended without aliasing src.
func appendCopy[T any](src []T, x T) []T {
	dst := make([]T, 0, len(src)+1)
	dst = append(dst, src...)
	dst = append(dst, x)
	return dst
}

type sigKey struct {
	subject SubjectID
	lt      LinkType
	from    ObjectType
	to      ObjectType
}

type stepKey struct {
	from ObjectID
	to   ObjectID
	lt   LinkType
}

// searchContext shares pinned snapshots, the type-signature verdict cache and
// the per-candidate trace across the pessimistic/optimistic passes.
type searchContext struct {
	q     Query
	r     *resolver
	gs    *graphSnapshot
	cache map[sigKey]VerdictResult
	seen  map[stepKey]bool
	cnt   Counters
	steps []StepDecision
}

// evaluate runs the overlay rules for one candidate link, memoizing by its
// type signature and recording the candidate exactly once.
func (c *searchContext) evaluate(cur ObjectID, ed edge) VerdictResult {
	key := sigKey{lt: ed.link.Type, from: c.gs.objects[cur].Type, to: ed.t}
	key.subject = c.q.Subject
	sk := stepKey{from: cur, to: ed.to, lt: ed.link.Type}
	v, ok := c.cache[key]
	if !ok {
		v = c.r.resolve(c.q.Subject, key.lt, key.from, key.to)
		c.cache[key] = v
		c.cnt.LinkEvaluations++
	}
	if !c.seen[sk] {
		// Each distinct candidate link is measured once regardless of how
		// many search passes examine it.
		c.seen[sk] = true
		c.cnt.Relaxations++
		c.cnt.EndpointTypeLookups++
		c.steps = append(c.steps, StepDecision{
			From: cur, To: ed.to, LinkType: ed.link.Type,
			LinkCost: ed.link.Cost, Verdict: v,
		})
	}
	return v
}

// dijkstra executes one tie-breaking Dijkstra pass. When ambAllowed is false
// ambiguous links are treated as deny (pessimistic); when true as allow
// (optimistic). Comparing the two passes separates genuine ambiguity from a
// definite unreachable.
func (c *searchContext) dijkstra(ambAllowed bool) *nodeState {
	best := map[ObjectID]*nodeState{}
	start := &nodeState{dist: 0, path: []ObjectID{c.q.From}}
	best[c.q.From] = start

	pq := &priorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &pqItem{id: c.q.From, dist: 0, path: start.path})

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*pqItem)
		state := best[cur.id]
		if cur.dist != state.dist || !sameSeq(cur.path, state.path) {
			continue
		}
		c.cnt.NodesSettled++
		if cur.id == c.q.To {
			return state
		}

		for _, ed := range c.gs.out[cur.id] {
			v := c.evaluate(cur.id, ed)
			traversable := v.Verdict == VerdictAllow || (ambAllowed && v.Verdict == VerdictAmbiguous)
			if !traversable {
				continue
			}
			nd := cur.dist + ed.link.Cost
			npath := appendCopy(cur.path, ed.to)
			nlinks := appendCopy(state.links, ed.link.Type)

			existing, seen := best[ed.to]
			if !seen || nd < existing.dist || (nd == existing.dist && lexLess(npath, existing.path)) {
				best[ed.to] = &nodeState{dist: nd, path: npath, links: nlinks}
				heap.Push(pq, &pqItem{id: ed.to, dist: nd, path: npath})
			}
		}
	}
	return nil
}

// ShortestPath returns the minimum-cost allowed path, breaking ties by the
// lexicographically smallest sequence of visited object IDs.
func (e *QueryEngine) ShortestPath(q Query) QueryResult {
	// Both snapshots are pinned before any edge is examined; concurrent
	// mutations afterwards are invisible to this query.
	permSnap := e.state.Snapshot()
	gs := e.graph.snapshot()
	return e.shortestPathAt(q, permSnap, gs)
}

func (e *QueryEngine) shortestPathAt(q Query, permSnap *snapshot, gs *graphSnapshot) QueryResult {
	res := QueryResult{StateVersion: permSnap.version, GraphVersion: gs.version}
	if !ValidSubject(q.Subject) {
		res.Status = StatusInvalidSubject
		return res
	}
	if _, ok := gs.objects[q.From]; !ok {
		res.Status = StatusMissingObject
		return res
	}
	if _, ok := gs.objects[q.To]; !ok {
		res.Status = StatusMissingObject
		return res
	}

	c := &searchContext{
		q:     q,
		r:     newResolver(permSnap),
		gs:    gs,
		cache: map[sigKey]VerdictResult{},
		seen:  map[stepKey]bool{},
	}

	// Pessimistic pass first: a route built only from definitively allowed
	// links is the guaranteed answer unless the optimistic pass proves that an
	// ambiguous edge yields a different route, in which case no unique answer
	// exists.
	safe := c.dijkstra(false)
	hopeful := c.dijkstra(true)

	res.Counters = c.cnt
	res.Steps = c.steps

	switch {
	case safe != nil && hopeful != nil && sameSeq(safe.path, hopeful.path):
		res.Status = StatusReachable
		res.Path = toPath(safe)
	case safe != nil:
		// A cheaper or lex-smaller route relies on an ambiguous link.
		res.Status = StatusAmbiguous
	case hopeful != nil:
		res.Status = StatusAmbiguous
	default:
		res.Status = StatusUnreachable
	}
	return res
}

func toPath(s *nodeState) *Path {
	return &Path{
		ObjectIDs: append([]ObjectID(nil), s.path...),
		LinkTypes: append([]LinkType(nil), s.links...),
		Cost:      s.dist,
	}
}

func sameSeq(a, b []ObjectID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
