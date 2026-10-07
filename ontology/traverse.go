package ontology

import (
	"sort"
)

// TerminalReason classifies why a path terminated.
type TerminalReason int

const (
	TerminatedBoundary TerminalReason = iota
	TerminatedCycle
	TerminatedDepthLimit
)

// String renders the terminal classification for logs and diagnostics.
func (r TerminalReason) String() string {
	switch r {
	case TerminatedBoundary:
		return "boundary"
	case TerminatedCycle:
		return "cycle"
	case TerminatedDepthLimit:
		return "depth-limit"
	}
	return "unknown"
}

// TraverseRequest is the input of one traversal.
type TraverseRequest struct {
	Start     ObjectID
	LinkTypes map[LinkType]Direction
	MaxDepth  int
}

// Path is one complete root-to-terminal path returned by the traversal.
type Path struct {
	// Nodes is the root-to-terminal object sequence (start included).
	Nodes []ObjectID
	// Links[i] is the link traversed from Nodes[i] to Nodes[i+1].
	Links []LinkID
	// Reason is the terminal state of exactly this path.
	Reason TerminalReason
	// Ancestors is the per-hop ancestor sequence used for the final decision.
	Ancestors []ObjectID
}

// TraverseResult is the full output of one traversal.
type TraverseResult struct {
	Paths []Path
	// AncestorChecks is the total number of ancestor-sequence membership
	// probes performed during this traversal. Each probe is O(1).
	AncestorChecks int
}

// Logger records per-traversal diagnostics.
type Logger interface {
	LogTraversal(req TraverseRequest, result TraverseResult)
}

// TraverseService executes traversals against a graph.
type TraverseService struct {
	graph  Graph
	logger Logger
}

// NewTraverseService constructs a TraverseService.
func NewTraverseService(g Graph, logger Logger) *TraverseService {
	return &TraverseService{graph: g, logger: logger}
}

// Traverse runs one traversal.
//
// Validation errors are reported in a fixed, mutually exclusive order:
// ErrStartObjectNotFound, then ErrEmptyOrUndefinedLinkTypes, then
// ErrInvalidMaxDepth. A rejected request produces no partial result.
func (s *TraverseService) Traverse(req TraverseRequest) (TraverseResult, error) {
	snap := s.graph.Snapshot()
	res, err := TraverseOnSnapshot(snap, req)
	if err != nil {
		return TraverseResult{}, err
	}
	if s.logger != nil {
		s.logger.LogTraversal(req, res)
	}
	return res, nil
}

// TraverseOnSnapshot runs the production traversal against an explicit
// snapshot. Taking the snapshot is the callers responsibility; everything
// below this line only reads immutable snapshot data.
func TraverseOnSnapshot(snap Snapshot, req TraverseRequest) (TraverseResult, error) {
	if !snap.HasObject(req.Start) {
		return TraverseResult{}, ErrStartObjectNotFound
	}
	if err := req.validate(snap); err != nil {
		return TraverseResult{}, err
	}

	t := &traversal{
		snap: snap,
		req:  req,
	}
	t.expand(t.req.Start, []ObjectID{t.req.Start}, map[ObjectID]struct{}{t.req.Start: {}}, nil)

	// Deterministic output independent of internal scheduling: sort by node
	// sequence, then by link sequence.
	sort.SliceStable(t.result.Paths, func(i, j int) bool {
		return comparePath(t.result.Paths[i], t.result.Paths[j]) < 0
	})

	return t.result, nil
}

// validate checks direction-set and depth constraints against a snapshot.
func (req TraverseRequest) validate(snap Snapshot) error {
	if len(req.LinkTypes) == 0 {
		return ErrEmptyOrUndefinedLinkTypes
	}
	for t, dir := range req.LinkTypes {
		if !snap.HasLinkType(t) || (dir != DirectionOut && dir != DirectionIn) {
			return ErrEmptyOrUndefinedLinkTypes
		}
	}
	if req.MaxDepth <= 0 {
		return ErrInvalidMaxDepth
	}
	return nil
}

// traversal holds mutable per-traversal state. Crucially it contains NO
// global "already visited objects" set: diamond merges must keep expanding.
type traversal struct {
	snap   Snapshot
	req    TraverseRequest
	result TraverseResult
}

// expand performs a depth-first walk. nodeSeq/linkSeq are the current path,
// ancestor is its set view. A single set is shared across sibling branches
// and maintained by backtracking (add on descent, remove on return), so
// each hop performs O(1) set work rather than copying the full chain.
func (t *traversal) expand(current ObjectID, nodeSeq []ObjectID, ancestor map[ObjectID]struct{}, linkSeq []LinkID) {
	refs := t.neighbors(current)
	if len(refs) == 0 {
		t.terminate(nodeSeq, linkSeq, TerminatedBoundary)
		return
	}

	depth := len(nodeSeq) - 1 // hops already taken from the start
	for _, ref := range refs {
		// The ancestor probe: one O(1) map lookup per candidate edge.
		t.result.AncestorChecks++

		// Snapshot the decision basis before appending the child, so the
		// recorded ancestor sequence is exactly what was probed.
		ancestorsAtDecision := append([]ObjectID(nil), nodeSeq...)

		// Cycle wins over depth truncation, always and for every input:
		// a self-loop on the first hop is therefore a cycle, not a truncation.
		if _, onAncestorChain := ancestor[ref.Target]; onAncestorChain {
			nodes := append(append([]ObjectID(nil), nodeSeq...), ref.Target)
			links := append(append([]LinkID(nil), linkSeq...), ref.LinkID)
			t.terminateWith(nodes, links, TerminatedCycle, ancestorsAtDecision)
			continue
		}

		// Not a cycle; check the depth limit on this hop.
		if depth+1 >= t.req.MaxDepth {
			nodes := append(append([]ObjectID(nil), nodeSeq...), ref.Target)
			links := append(append([]LinkID(nil), linkSeq...), ref.LinkID)
			t.terminateWith(nodes, links, TerminatedDepthLimit, ancestorsAtDecision)
			continue
		}

		// Independent parallel links each get their own child frame.
		nodeSeq = append(nodeSeq, ref.Target)
		linkSeq = append(linkSeq, ref.LinkID)
		ancestor[ref.Target] = struct{}{}
		t.expand(ref.Target, nodeSeq, ancestor, linkSeq)
		// Backtrack before the next parallel sibling is judged.
		delete(ancestor, ref.Target)
		nodeSeq = nodeSeq[:len(nodeSeq)-1]
		linkSeq = linkSeq[:len(linkSeq)-1]
	}
}

// neighbors gathers candidate edges across all configured (type, direction)
// pairs. Parallel links survive as distinct refs.
func (t *traversal) neighbors(current ObjectID) []LinkRef {
	keys := make([]LinkType, 0, len(t.req.LinkTypes))
	for k := range t.req.LinkTypes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var refs []LinkRef
	for _, k := range keys {
		refs = append(refs, t.snap.Neighbors(current, k, t.req.LinkTypes[k])...)
	}
	// Neighbors per type are already LinkID-ordered; merge groups by
	// re-sorting for a fully deterministic order across types.
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].LinkID < refs[j].LinkID })
	return refs
}

func (t *traversal) terminate(nodeSeq []ObjectID, linkSeq []LinkID, reason TerminalReason) {
	t.terminateWith(append([]ObjectID(nil), nodeSeq...), append([]LinkID(nil), linkSeq...), reason, append([]ObjectID(nil), nodeSeq...))
}

func (t *traversal) terminateWith(nodes []ObjectID, links []LinkID, reason TerminalReason, ancestors []ObjectID) {
	t.result.Paths = append(t.result.Paths, Path{
		Nodes:     nodes,
		Links:     links,
		Reason:    reason,
		Ancestors: ancestors,
	})
}

func comparePath(a, b Path) int {
	if c := compareSeq(a.Nodes, b.Nodes); c != 0 {
		return c
	}
	return compareIDSeq(a.Links, b.Links)
}

func compareSeq(a, b []ObjectID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func compareIDSeq(a, b []LinkID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
