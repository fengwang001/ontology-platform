package ontology

import (
	"errors"
	"sync"
)

var (
	// ErrEmptyObjectID is returned when an object id is empty.
	ErrEmptyObjectID = errors.New("ontology: empty object id")
	// ErrUnknownObject is returned when a referenced object does not exist.
	ErrUnknownObject = errors.New("ontology: unknown object")
	// ErrNegativeCost is returned when a link cost is negative.
	ErrNegativeCost = errors.New("ontology: link cost must be non-negative")
)

type (
	// ObjectID identifies an ontology object.
	ObjectID string
)

// Object is a typed ontology node.
type Object struct {
	ID   ObjectID
	Type ObjectType
}

// Link is a directed, typed, weighted edge between two objects.
type Link struct {
	Type LinkType
	From ObjectID
	To   ObjectID
	Cost float64
}

// edge is the internal adjacency representation.
type edge struct {
	link Link
	to   ObjectID
	t    ObjectType
}

// Graph is a directed ontology link graph, safe for concurrent use.
type Graph struct {
	mu      sync.RWMutex
	version int64
	objects map[ObjectID]Object
	out     map[ObjectID][]edge
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		objects: map[ObjectID]Object{},
		out:     map[ObjectID][]edge{},
	}
}

// AddObject creates or replaces an object with the given type.
func (g *Graph) AddObject(id ObjectID, t ObjectType) error {
	if id == "" {
		return ErrEmptyObjectID
	}
	if t == "" {
		return ErrEmptyID
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.objects[id] = Object{ID: id, Type: t}
	g.version++
	return nil
}

// AddLink adds a directed typed weighted link. Link triples
// (From, To, Type) are unique; a re-add replaces the cost. Both endpoints
// must already exist and the cost must be non-negative.
func (g *Graph) AddLink(l Link) error {
	if l.Type == "" {
		return ErrEmptyID
	}
	if l.From == "" || l.To == "" {
		return ErrEmptyObjectID
	}
	if l.Cost < 0 {
		return ErrNegativeCost
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[l.From]; !ok {
		return ErrUnknownObject
	}
	to, ok := g.objects[l.To]
	if !ok {
		return ErrUnknownObject
	}
	edges := g.out[l.From]
	for i := range edges {
		if edges[i].link.Type == l.Type && edges[i].to == l.To {
			edges[i].link.Cost = l.Cost
			g.version++
			return nil
		}
	}
	g.out[l.From] = append(edges, edge{link: l, to: l.To, t: to.Type})
	g.version++
	return nil
}

// RemoveLink deletes the link identified by (from, to, typ), if present.
func (g *Graph) RemoveLink(from, to ObjectID, typ LinkType) {
	g.mu.Lock()
	defer g.mu.Unlock()
	edges := g.out[from]
	kept := edges[:0]
	removed := false
	for _, e := range edges {
		if !removed && e.to == to && e.link.Type == typ {
			removed = true
			continue
		}
		kept = append(kept, e)
	}
	if removed {
		g.out[from] = kept
		g.version++
	}
}

// RemoveObject deletes an object and every link touching it.
func (g *Graph) RemoveObject(id ObjectID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[id]; !ok {
		return
	}
	delete(g.objects, id)
	delete(g.out, id)
	for from, edges := range g.out {
		kept := edges[:0]
		for _, e := range edges {
			if e.to != id {
				kept = append(kept, e)
			}
		}
		g.out[from] = kept
	}
	g.version++
}

// snapshot returns an immutable point-in-time view of the graph.
func (g *Graph) snapshot() *graphSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	objects := make(map[ObjectID]Object, len(g.objects))
	for id, o := range g.objects {
		objects[id] = o
	}
	out := make(map[ObjectID][]edge, len(g.out))
	for id, edges := range g.out {
		cp := make([]edge, len(edges))
		copy(cp, edges)
		out[id] = cp
	}
	return &graphSnapshot{version: g.version, objects: objects, out: out}
}

// graphSnapshot is an immutable point-in-time view of the graph.
type graphSnapshot struct {
	version int64
	objects map[ObjectID]Object
	out     map[ObjectID][]edge
}

func (gs *graphSnapshot) objectType(id ObjectID) (ObjectType, bool) {
	o, ok := gs.objects[id]
	if !ok {
		return "", false
	}
	return o.Type, true
}
