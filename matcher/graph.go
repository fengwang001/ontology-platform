package matcher

import (
	"sort"
	"sync"
)

// Object is a node in the object graph.
type Object struct {
	ID    string
	Type  string
	Attrs map[string]any
}

// Edge is a directed typed link between two objects.
type Edge struct {
	Type   string
	FromID string
	ToID   string
}

type edgeKey struct {
	from, typ, to string
}

// Graph is a concurrency-safe object graph.
type Graph struct {
	// objects and indexes are only mutated under mu. Object slices stored in
	// byType are always kept sorted, so results never depend on insertion order.
	mu      sync.RWMutex
	objects map[string]Object
	byType  map[string][]string
	edges   map[edgeKey]struct{}
	// out/in adjacency: objectID -> edgeType -> set of neighbor IDs
	out map[string]map[string]map[string]struct{}
	in  map[string]map[string]map[string]struct{}
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		objects: map[string]Object{},
		byType:  map[string][]string{},
		edges:   map[edgeKey]struct{}{},
		out:     map[string]map[string]map[string]struct{}{},
		in:      map[string]map[string]map[string]struct{}{},
	}
}

// AddObject inserts a node.
func (g *Graph) AddObject(o Object) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.objects[o.ID]; exists {
		return
	}
	g.objects[o.ID] = o
	ids := append(g.byType[o.Type], o.ID)
	sort.Strings(ids)
	g.byType[o.Type] = ids
}

// AddEdge inserts a directed edge.
func (g *Graph) AddEdge(e Edge) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, fromOK := g.objects[e.FromID]; !fromOK {
		return
	}
	if _, toOK := g.objects[e.ToID]; !toOK {
		return
	}
	key := edgeKey{from: e.FromID, typ: e.Type, to: e.ToID}
	if _, dup := g.edges[key]; dup {
		return
	}
	g.edges[key] = struct{}{}
	addAdj(g.out, e.FromID, e.Type, e.ToID)
	addAdj(g.in, e.ToID, e.Type, e.FromID)
}

func addAdj(adj map[string]map[string]map[string]struct{}, id, edgeType, neighbor string) {
	byType, ok := adj[id]
	if !ok {
		byType = map[string]map[string]struct{}{}
		adj[id] = byType
	}
	set, ok := byType[edgeType]
	if !ok {
		set = map[string]struct{}{}
		byType[edgeType] = set
	}
	set[neighbor] = struct{}{}
}

// ObjectsOfType returns object IDs with the given type in a stable order.
func (g *Graph) ObjectsOfType(typ string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := g.byType[typ]
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// HasEdge reports whether a typed edge exists between two objects.
func (g *Graph) HasEdge(fromID, edgeType, toID string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.edges[edgeKey{from: fromID, typ: edgeType, to: toID}]
	return ok
}

// object returns a stored object by ID. The zero value is returned if absent.
func (g *Graph) object(id string) Object {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.objects[id]
}

// neighborsOut returns sorted targets of typed outgoing edges from fromID.
func (g *Graph) neighborsOut(fromID, edgeType string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return sortedSet(g.out[fromID][edgeType])
}

// neighborsIn returns sorted sources of typed incoming edges to toID.
func (g *Graph) neighborsIn(toID, edgeType string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return sortedSet(g.in[toID][edgeType])
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
