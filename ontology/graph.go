package ontology

import (
	"fmt"
	"slices"
	"sync"
)

// Graph is the main ontology graph: a versioned, concurrency-safe store of
// object types, link types, objects and links.
//
// Every successful mutating API call advances Epoch by exactly one and appends
// one or more Change entries to an append-only journal, all tagged with that
// new epoch. Reads do not advance the epoch.
type Graph struct {
	mu sync.RWMutex

	epoch   uint64
	journal []Change

	objectTypes map[TypeName]ObjectType
	linkTypes   map[TypeName]LinkType

	objects map[ObjectID]Object
	links   map[linkKey]Link

	// adjacency maps an object id to the set of link keys incident to it.
	// It is the reason Extract never scans the whole graph.
	adjacency map[ObjectID]map[linkKey]struct{}
}

// linkKey is the canonical storage identity of a link.
type linkKey struct {
	typ  TypeName
	a, b ObjectID
}

// canonicalKey canonicalizes a link: for undirected link types the smaller id
// is stored first, so {A,B} and {B,A} are the same link.
func canonicalKey(t LinkType, src, sink ObjectID) linkKey {
	a, b := src, sink
	if t.Direct == Undirected && b < a {
		a, b = b, a
	}
	return linkKey{typ: t.Name, a: a, b: b}
}

// incident reports whether key k touches object id.
func (k linkKey) incident(id ObjectID) (other ObjectID, ok bool) {
	switch id {
	case k.a:
		return k.b, true
	case k.b:
		return k.a, true
	default:
		return "", false
	}
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		objectTypes: make(map[TypeName]ObjectType),
		linkTypes:   make(map[TypeName]LinkType),
		objects:     make(map[ObjectID]Object),
		links:       make(map[linkKey]Link),
		adjacency:   make(map[ObjectID]map[linkKey]struct{}),
	}
}

// AddObjectType registers an object type. Registering the same name twice with
// an identical definition is idempotent.
func (g *Graph) AddObjectType(t ObjectType) error {
	if !validTypeName(t.Name) {
		return invalidArgumentf("invalid object type name %q", t.Name)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.objectTypes[t.Name]; ok && existing != t {
		return fmt.Errorf("%w: object type %q already defined differently", ErrInvalidArgument, t.Name)
	}
	g.objectTypes[t.Name] = t
	return nil
}

// AddLinkType registers a link type.
func (g *Graph) AddLinkType(t LinkType) error {
	if !validTypeName(t.Name) {
		return invalidArgumentf("invalid link type name %q", t.Name)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.linkTypes[t.Name]; ok && existing != t {
		return fmt.Errorf("%w: link type %q already defined differently", ErrInvalidArgument, t.Name)
	}
	if _, ok := g.objectTypes[t.Source]; !ok {
		return invalidArgumentf("unknown source object type %q", t.Source)
	}
	if _, ok := g.objectTypes[t.Sink]; !ok {
		return invalidArgumentf("unknown sink object type %q", t.Sink)
	}
	if t.Direct != Directed && t.Direct != Undirected {
		return invalidArgumentf("invalid direction %d for link type %q", t.Direct, t.Name)
	}
	g.linkTypes[t.Name] = t
	return nil
}

// AddObject inserts an object. Readers hold existence permission: a caller may
// see the object iff it is listed in Readers.
func (g *Graph) AddObject(o Object) error {
	if !validObjectID(o.ID) {
		return invalidArgumentf("invalid object id %q", o.ID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[o.Type]; !ok {
		return invalidArgumentf("unknown object type %q", o.Type)
	}
	if _, exists := g.objects[o.ID]; exists {
		return fmt.Errorf("%w: object %q", ErrInvalidArgument, o.ID)
	}
	o.Readers = slices.Clone(o.Readers)
	g.objects[o.ID] = o
	g.adjacency[o.ID] = make(map[linkKey]struct{})
	g.epoch++
	g.journal = append(g.journal, Change{Epoch: g.epoch, Kind: ObjectAdded, Object: o.ID})
	return nil
}

// RemoveObject deletes an object together with every incident link (cascade).
// All deletions journal under one new epoch.
func (g *Graph) RemoveObject(id ObjectID) error {
	if !validObjectID(id) {
		return invalidArgumentf("invalid object id %q", id)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[id]; !ok {
		return fmt.Errorf("%w: object %q", ErrNotFound, id)
	}
	g.epoch++
	for key := range g.adjacency[id] {
		link := g.links[key]
		g.deleteLinkLocked(key)
		g.journal = append(g.journal, Change{Epoch: g.epoch, Kind: LinkRemoved, Link: ptrLink(link)})
	}
	delete(g.adjacency, id)
	delete(g.objects, id)
	g.journal = append(g.journal, Change{Epoch: g.epoch, Kind: ObjectRemoved, Object: id})
	return nil
}

// AddLink inserts a link. Both endpoint objects must exist and their types
// must match the link type. Re-adding an identical link is rejected.
func (g *Graph) AddLink(l Link) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.linkTypes[l.Type]
	if !ok {
		return invalidArgumentf("unknown link type %q", l.Type)
	}
	src, ok := g.objects[l.Source]
	if !ok {
		return fmt.Errorf("%w: source object %q", ErrNotFound, l.Source)
	}
	sink, ok := g.objects[l.Sink]
	if !ok {
		return fmt.Errorf("%w: sink object %q", ErrNotFound, l.Sink)
	}
	if !linkEndpointsMatch(t, src.Type, sink.Type) {
		return fmt.Errorf("%w: link %q cannot connect %q -> %q", ErrTypeMismatch, l.Type, src.Type, sink.Type)
	}
	key := canonicalKey(t, l.Source, l.Sink)
	if _, exists := g.links[key]; exists {
		return fmt.Errorf("%w: link %v", ErrInvalidArgument, l)
	}
	g.links[key] = Link{Type: l.Type, Source: l.Source, Sink: l.Sink}
	g.addAdjacency(key, l.Source, l.Sink)
	g.epoch++
	g.journal = append(g.journal, Change{Epoch: g.epoch, Kind: LinkAdded, Link: ptrLink(g.links[key])})
	return nil
}

// RemoveLink removes a single link.
func (g *Graph) RemoveLink(l Link) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.linkTypes[l.Type]
	if !ok {
		return invalidArgumentf("unknown link type %q", l.Type)
	}
	key := canonicalKey(t, l.Source, l.Sink)
	stored, ok := g.links[key]
	if !ok {
		return fmt.Errorf("%w: link %v", ErrNotFound, l)
	}
	g.epoch++
	g.deleteLinkLocked(key)
	g.journal = append(g.journal, Change{Epoch: g.epoch, Kind: LinkRemoved, Link: ptrLink(stored)})
	return nil
}

// Epoch returns the current version of the graph.
func (g *Graph) Epoch() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.epoch
}

// Journal returns a defensive copy of all changes recorded so far, ordered by
// (epoch, within-epoch append order).
func (g *Graph) Journal() []Change {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Change, len(g.journal))
	copy(out, g.journal)
	for i := range out {
		if g.journal[i].Link != nil {
			l := *g.journal[i].Link
			out[i].Link = &l
		}
	}
	return out
}

// canSeeLocked reports whether caller has existence permission on object id.
// Caller must hold g.mu (read or write).
func (g *Graph) canSeeLocked(caller Principal, id ObjectID) bool {
	o, ok := g.objects[id]
	if !ok {
		return false
	}
	return slices.Contains(o.Readers, caller)
}

// linkEndpointsMatch checks endpoint object types against the link type.
func linkEndpointsMatch(t LinkType, srcType, sinkType TypeName) bool {
	if srcType == t.Source && sinkType == t.Sink {
		return true
	}
	// An undirected type {X,Y} may also be instantiated in reverse order.
	if t.Direct == Undirected && srcType == t.Sink && sinkType == t.Source {
		return true
	}
	return false
}

func (g *Graph) addAdjacency(key linkKey, a, b ObjectID) {
	if g.adjacency[a] == nil {
		g.adjacency[a] = make(map[linkKey]struct{})
	}
	g.adjacency[a][key] = struct{}{}
	if b != a {
		if g.adjacency[b] == nil {
			g.adjacency[b] = make(map[linkKey]struct{})
		}
		g.adjacency[b][key] = struct{}{}
	}
}

func (g *Graph) deleteLinkLocked(key linkKey) {
	delete(g.adjacency[key.a], key)
	if key.b != key.a {
		delete(g.adjacency[key.b], key)
	}
	delete(g.links, key)
}

func ptrLink(l Link) *Link { l2 := l; return &l2 }
