package ontology

import (
	"sort"
	"sync"
	"sync/atomic"
)

// LinkID uniquely identifies a link in the graph.
type LinkID string

// ObjectID uniquely identifies an object in the graph.
type ObjectID string

// LinkType is the type label of a link.
type LinkType string

// Direction selects how a link type is traversed.
type Direction int

const (
	DirectionOut Direction = iota
	DirectionIn
)

// Link is a single directed edge.
type Link struct {
	ID     LinkID
	Type   LinkType
	Source ObjectID
	Target ObjectID
}

// Snapshot is an immutable read-only view of the graph at one point in time.
type Snapshot interface {
	HasObject(id ObjectID) bool
	HasLinkType(t LinkType) bool
	Neighbors(from ObjectID, t LinkType, dir Direction) []LinkRef
}

// LinkRef is one outgoing adjacency entry.
type LinkRef struct {
	LinkID LinkID
	Target ObjectID
}

// Graph is a mutable graph that supports snapshot isolation.
type Graph interface {
	Snapshot() Snapshot
	DefineLinkType(t LinkType)
	AddObject(id ObjectID) error
	AddLink(link Link) error
	DeleteLink(id LinkID) error
}

// graphState is immutable once published through atomic.Pointer.
// Every mutating operation copy-on-writes the maps before changing them.
type graphState struct {
	objects map[ObjectID]struct{}
	types   map[LinkType]struct{}
	links   map[LinkID]Link
	out     map[ObjectID]map[LinkType][]LinkRef
	in      map[ObjectID]map[LinkType][]LinkRef
}

func newGraphState() *graphState {
	return &graphState{
		objects: map[ObjectID]struct{}{},
		types:   map[LinkType]struct{}{},
		links:   map[LinkID]Link{},
		out:     map[ObjectID]map[LinkType][]LinkRef{},
		in:      map[ObjectID]map[LinkType][]LinkRef{},
	}
}

// clone shallow-copies every map. Published states are never mutated, so
// sharing values with the clone is safe.
func (s *graphState) clone() *graphState {
	cp := &graphState{
		objects: make(map[ObjectID]struct{}, len(s.objects)),
		types:   make(map[LinkType]struct{}, len(s.types)),
		links:   make(map[LinkID]Link, len(s.links)),
		out:     make(map[ObjectID]map[LinkType][]LinkRef, len(s.out)),
		in:      make(map[ObjectID]map[LinkType][]LinkRef, len(s.in)),
	}
	for k := range s.objects {
		cp.objects[k] = struct{}{}
	}
	for k := range s.types {
		cp.types[k] = struct{}{}
	}
	for k, v := range s.links {
		cp.links[k] = v
	}
	for obj, byType := range s.out {
		m := make(map[LinkType][]LinkRef, len(byType))
		for t, refs := range byType {
			m[t] = append([]LinkRef(nil), refs...)
		}
		cp.out[obj] = m
	}
	for obj, byType := range s.in {
		m := make(map[LinkType][]LinkRef, len(byType))
		for t, refs := range byType {
			m[t] = append([]LinkRef(nil), refs...)
		}
		cp.in[obj] = m
	}
	return cp
}

// MemGraph is a concurrent, snapshot-isolated in-memory graph.
type MemGraph struct {
	mu    sync.Mutex
	state atomic.Pointer[graphState]
}

// NewMemGraph creates an empty graph.
func NewMemGraph() *MemGraph {
	g := &MemGraph{}
	g.state.Store(newGraphState())
	return g
}

// Snapshot returns an O(1), immutable point-in-time view. It never blocks
// concurrent AddLink/DeleteLink operations and vice versa.
func (g *MemGraph) Snapshot() Snapshot {
	return &memSnapshot{st: g.state.Load()}
}

// DefineLinkType registers a link type as defined on the graph.
func (g *MemGraph) DefineLinkType(t LinkType) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.state.Load().clone()
	st.types[t] = struct{}{}
	g.state.Store(st)
}

// AddObject inserts an object.
func (g *MemGraph) AddObject(id ObjectID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.state.Load().clone()
	if _, ok := st.objects[id]; ok {
		return ErrDuplicateObject
	}
	st.objects[id] = struct{}{}
	g.state.Store(st)
	return nil
}

// AddLink inserts a link. Source and target must exist, link ID must be
// unique, direction type must be registered through DefineLinkType.
func (g *MemGraph) AddLink(link Link) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.state.Load().clone()
	if _, ok := st.objects[link.Source]; !ok {
		return ErrObjectNotFound
	}
	if _, ok := st.objects[link.Target]; !ok {
		return ErrObjectNotFound
	}
	if _, ok := st.links[link.ID]; ok {
		return ErrDuplicateLink
	}
	st.links[link.ID] = link
	ref := LinkRef{LinkID: link.ID, Target: link.Target}
	g.appendRef(st.out, link.Source, link.Type, ref)
	g.appendRef(st.in, link.Target, link.Type, LinkRef{LinkID: link.ID, Target: link.Source})
	g.state.Store(st)
	return nil
}

// DeleteLink removes a link by ID. It is a no-op returning false when absent.
func (g *MemGraph) DeleteLink(id LinkID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.state.Load().clone()
	link, ok := st.links[id]
	if !ok {
		return ErrLinkNotFound
	}
	delete(st.links, id)
	g.removeRef(st.out, link.Source, link.Type, id)
	g.removeRef(st.in, link.Target, link.Type, id)
	g.state.Store(st)
	return nil
}

func (g *MemGraph) appendRef(adj map[ObjectID]map[LinkType][]LinkRef, obj ObjectID, t LinkType, ref LinkRef) {
	byType, ok := adj[obj]
	if !ok {
		byType = map[LinkType][]LinkRef{}
		adj[obj] = byType
	}
	refs := byType[t]
	refs = append(refs, ref)
	sort.Slice(refs, func(i, j int) bool { return refs[i].LinkID < refs[j].LinkID })
	byType[t] = refs
}

func (g *MemGraph) removeRef(adj map[ObjectID]map[LinkType][]LinkRef, obj ObjectID, t LinkType, id LinkID) {
	byType, ok := adj[obj]
	if !ok {
		return
	}
	refs := byType[t]
	for i, r := range refs {
		if r.LinkID == id {
			byType[t] = append(refs[:i], refs[i+1:]...)
			return
		}
	}
}

// memSnapshot wraps one immutable graphState.
type memSnapshot struct {
	st *graphState
}

func (s *memSnapshot) HasObject(id ObjectID) bool {
	_, ok := s.st.objects[id]
	return ok
}

func (s *memSnapshot) HasLinkType(t LinkType) bool {
	_, ok := s.st.types[t]
	return ok
}

// Neighbors returns a fresh slice of the parallel links of type t from obj
// in direction dir, ordered deterministically by LinkID.
func (s *memSnapshot) Neighbors(obj ObjectID, t LinkType, dir Direction) []LinkRef {
	var adj map[ObjectID]map[LinkType][]LinkRef
	switch dir {
	case DirectionOut:
		adj = s.st.out
	case DirectionIn:
		adj = s.st.in
	default:
		return nil
	}
	byType, ok := adj[obj]
	if !ok {
		return nil
	}
	return append([]LinkRef(nil), byType[t]...)
}
