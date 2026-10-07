package ontology

// Outcome is the tri-state result of a reachability query.
type Outcome int

const (
	// OutcomeUnknown is the zero value and is never returned by a finished query.
	OutcomeUnknown Outcome = iota
	// Reachable: at least one fully visible, fully traversable path exists.
	Reachable
	// Unreachable: no such path exists, and both endpoints are visible.
	Unreachable
	// Restricted: invisibility or a traversal cut prevents proving reachability
	// or non-existence, so the answer is "unknown due to permissions".
	Restricted
)

func (o Outcome) String() string {
	switch o {
	case Reachable:
		return "reachable"
	case Unreachable:
		return "unreachable"
	case Restricted:
		return "restricted_unknown"
	default:
		return "unknown"
	}
}

// Metrics counts only objects/links this query actually attempted to expand.
// It is an internal, verifiable cost measure and is never exposed through
// any caller-facing API surface other than an explicitly internal trace.
type Metrics struct {
	// ObjectsExpanded counts unique objects dequeued in the forward search.
	ObjectsExpanded int
	// LinksAttempted counts arcs examined (permission tested) forward.
	LinksAttempted int
	// BacktrackObjects / BacktrackLinks count work in the internal pruning
	// oracle (reverse closure used to classify denied arcs).
	BacktrackObjects int
	BacktrackLinks   int
}

// Trace records one query's inputs, classification basis and internal metrics.
type Trace struct {
	From        ID
	To          ID
	Caller      ID
	Outcome     Outcome
	Reason      string
	SnapshotSeq uint64
	Metrics     Metrics
	// DeniedCandidates lists traversal-blocked arcs that could, with the
	// missing permission, lead toward the target.
	DeniedCandidates []DeniedArc
}

// DeniedArc is one blocked arc observed during the search.
type DeniedArc struct {
	LinkID   ID
	LinkType ID
	From     ID
	To       ID
}

// SearchHook is invoked after a query's snapshot is fixed but before the
// search runs. Mutations performed inside it are linearized strictly after
// this query's snapshot, modeling concurrent grant/revoke operations.
type SearchHook func()

// Reachable decides whether to is reachable from from for caller.
//
// Decision order:
//  1. invalid argument (illegal endpoint id) -> ErrInvalidID
//  2. endpoint genuinely absent from the graph -> ErrNotFound
//  3. endpoint invisible to caller (no existence permission) -> Restricted
//  4. tri-state graph search.
func (g *Graph) Reachable(from, to, caller ID) (Outcome, *Trace, error) {
	return g.ReachableWithHook(from, to, caller, nil)
}

// ReachableWithHook is Reachable with an optional snapshot-boundary hook.
//
// Serializability: the snapshot (graph + caller permission sets) is fixed
// while holding the graph's read lock. When hook is non-nil it is launched at
// exactly that instant and its mutations block behind the read lock until the
// search finishes, which is equivalent to placing the mutations immediately
// after this query in the single serial order. Concurrent callers without a
// hook observe the same guarantee because every mutation and every snapshot
// acquire the same lock.
func (g *Graph) ReachableWithHook(from, to, caller ID, hook SearchHook) (Outcome, *Trace, error) {
	if err := ValidateID(from); err != nil {
		return OutcomeUnknown, nil, err
	}
	if err := ValidateID(to); err != nil {
		return OutcomeUnknown, nil, err
	}
	if err := ValidateID(caller); err != nil {
		return OutcomeUnknown, nil, err
	}

	hookDone := make(chan struct{})
	g.mu.RLock()
	snap := g.takeSnapshot(caller)
	snap.seq = g.seq
	_, fromExists := g.objects[from]
	_, toExists := g.objects[to]
	if hook != nil {
		go func() {
			defer close(hookDone)
			// The mutation blocks here until the running query releases its
			// read lock, which is exactly the serial order:
			// ... query snapshot/search ..., revoke, later queries ...
			hook()
		}()
	}

	outcome, trace, err := g.searchLocked(from, to, fromExists, toExists, snap)
	g.mu.RUnlock()
	if hook != nil {
		// Any mutation accepted here is necessarily after this query's
		// snapshot; block returning until it is committed so tests can
		// assert the deterministic boundary.
		<-hookDone
	}
	return outcome, trace, err
}

// searchLocked runs the decision under g.mu (read held). It never mutates
// graph state and uses only the snapshot's permission view.
func (g *Graph) searchLocked(from, to ID, fromExists, toExists bool, snap snapshot) (Outcome, *Trace, error) {
	trace := &Trace{From: from, To: to, Caller: snap.caller, SnapshotSeq: snap.seq}

	// Decision order 2: genuine absence is independent of permissions.
	if !fromExists || !toExists {
		return OutcomeUnknown, nil, ErrNotFound
	}

	// Decision order 3: invisible endpoints are indistinguishable from
	// non-existent ones through any output, but existence is established
	// internally, so the tri-state answer is Restricted.
	if !snap.existence[from] || !snap.existence[to] {
		trace.Outcome = Restricted
		trace.Reason = "endpoint_invisible"
		return Restricted, trace, nil
	}

	if from == to {
		trace.Outcome = Reachable
		trace.Reason = "same_visible_object"
		trace.Metrics.ObjectsExpanded = 1
		return Reachable, trace, nil
	}

	// Decision order 4: forward BFS from the start, using only traversable
	// arcs. Denied arcs are collected for classification.
	visited := map[ID]bool{from: true}
	queue := []ID{from}
	var denied []DeniedArc

	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		trace.Metrics.ObjectsExpanded++
		for _, a := range g.out[u] {
			trace.Metrics.LinksAttempted++
			if !snap.traversable[a.lt] {
				denied = append(denied, DeniedArc{LinkID: a.linkID, LinkType: a.lt, From: a.from, To: a.to})
				continue
			}
			if a.to == to {
				trace.Outcome = Reachable
				trace.Reason = "fully_traversable_path_found"
				return Reachable, trace, nil
			}
			if !visited[a.to] {
				visited[a.to] = true
				queue = append(queue, a.to)
			}
		}
	}

	// No fully traversable path exists. Distinguish:
	//   Restricted  - some denied arc could, if permitted, lead to the target;
	//   Unreachable - even with unlimited traversal, none of the denied
	//                 frontier can reach the target and no hidden path exists.
	if len(denied) == 0 {
		trace.Outcome = Unreachable
		trace.Reason = "exhausted_search_no_candidate"
		return Unreachable, trace, nil
	}

	canReachTarget, backtrackMetrics := g.reverseClosure(to, denied, snap)
	trace.Metrics.BacktrackObjects = backtrackMetrics.objects
	trace.Metrics.BacktrackLinks = backtrackMetrics.links
	trace.DeniedCandidates = denied

	if canReachTarget {
		trace.Outcome = Restricted
		trace.Reason = "denied_arc_may_reach_target"
		return Restricted, trace, nil
	}
	trace.Outcome = Unreachable
	trace.Reason = "denied_arcs_cannot_reach_target"
	return Unreachable, trace, nil
}

type btMetrics struct{ objects, links int }

// reverseClosure answers whether the head of ANY denied arc can reach
// target in the permission-free graph (arcs reversed). It expands only
// backward from target: regions that cannot flow into target (e.g. pure
// directed sinks attached by a single into-region arc) are never entered,
// so this internal cost does not grow with their size.
//
// The snapshot is passed only to label work; closure intentionally ignores
// traversal permissions because the question is "what could reach target if
// the missing permission were granted".
func (g *Graph) reverseClosure(target ID, denied []DeniedArc, _ snapshot) (bool, btMetrics) {
	want := make(map[ID]bool, len(denied))
	for _, d := range denied {
		want[d.To] = true
	}
	var m btMetrics
	seen := map[ID]bool{target: true}
	queue := []ID{target}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		m.objects++
		if want[v] {
			return true, m
		}
		for _, a := range g.inArcs[v] {
			m.links++
			if !seen[a.from] {
				seen[a.from] = true
				queue = append(queue, a.from)
			}
		}
	}
	return false, m
}
