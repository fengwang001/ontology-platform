package graph

import "sync"

type Direction string

const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
)

type Link struct {
	FromID string
	ToID   string
	Type   string
}

type Path struct {
	ObjectIDs []string
	LinkTypes []string
}

type TruncationReason string

const (
	DepthOnly        TruncationReason = "depth_only"
	LimitOnly        TruncationReason = "limit_only"
	DepthBeforeLimit TruncationReason = "depth_before_limit"
	LimitBeforeDepth TruncationReason = "limit_before_depth"
)

type TraverseRequest struct {
	StartID     string
	MaxDepth    int
	ResultLimit int
	Directions  []Direction
	Logger      func(string, ...any)
}

type TruncatedBranch struct {
	Prefix           Path
	PrefixDepth      int
	Reason           TruncationReason
	DepthWhenStopped int
}

type TraverseResult struct {
	Paths          []Path
	Truncated      []TruncatedBranch
	SnapshotID     uint64
	CompletedPaths int
}

type Graph struct {
	mu       sync.RWMutex
	state    *graphState
	revision uint64
}

func NewGraph() *Graph {
	return &Graph{state: newGraphState()}
}

func (g *Graph) AddObject(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	next := g.state.clone()
	next.nodes[id] = struct{}{}
	if next.outgoing[id] == nil {
		next.outgoing[id] = make([]edge, 0)
	}
	if next.incoming[id] == nil {
		next.incoming[id] = make([]edge, 0)
	}
	g.state = next
	g.revision++
}

func (g *Graph) AddLink(link Link) {
	g.mu.Lock()
	defer g.mu.Unlock()

	next := g.state.clone()
	next.nodes[link.FromID] = struct{}{}
	next.nodes[link.ToID] = struct{}{}
	if next.outgoing[link.FromID] == nil {
		next.outgoing[link.FromID] = make([]edge, 0)
	}
	if next.incoming[link.ToID] == nil {
		next.incoming[link.ToID] = make([]edge, 0)
	}
	next.outgoing[link.FromID] = append(next.outgoing[link.FromID], edge{toID: link.ToID, linkType: link.Type})
	next.incoming[link.ToID] = append(next.incoming[link.ToID], edge{toID: link.FromID, linkType: link.Type})
	g.state = next
	g.revision++
}

func (g *Graph) Snapshot() *Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return &Snapshot{state: g.state, id: g.revision}
}

type Snapshot struct {
	state *graphState
	id    uint64
}
