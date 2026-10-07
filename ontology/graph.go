package ontology

import (
	"errors"
	"sync"
)

var (
	// ErrNotFound means no instance/type with the identifier exists in the graph.
	ErrNotFound = errors.New("ontology: not found")
	// ErrDuplicate means the identifier is already defined.
	ErrDuplicate = errors.New("ontology: duplicate identifier")
	// ErrTypeMismatch means an instance does not match its declared type.
	ErrTypeMismatch = errors.New("ontology: type mismatch")
)

// arc is one directed traversal opportunity derived from a link instance.
// For a bidirectional link, the reverse traversal (rev == true) is stored
// as a second arc.
type arc struct {
	linkID ID
	lt     ID
	from   ID
	to     ID
	rev    bool
}

// Graph is the in-memory ontology graph and permission store.
// All operations are serialized through mu, which is the single
// linearization point providing serializable snapshots for queries.
type Graph struct {
	mu sync.RWMutex

	seq         uint64
	objectTypes map[ID]ObjectType
	linkTypes   map[ID]LinkType
	objects     map[ID]ObjectInstance
	links       map[ID]LinkInstance

	// out[u] lists arcs leaving u; inArcs[v] lists arcs entering v.
	out    map[ID][]arc
	inArcs map[ID][]arc

	// existence[caller][objectID] grants knowledge that objectID exists.
	existence map[ID]map[ID]bool
	// traversable[caller][linkTypeID] grants traversal of a link type.
	traversable map[ID]map[ID]bool
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		objectTypes: map[ID]ObjectType{},
		linkTypes:   map[ID]LinkType{},
		objects:     map[ID]ObjectInstance{},
		links:       map[ID]LinkInstance{},
		out:         map[ID][]arc{},
		inArcs:      map[ID][]arc{},
		existence:   map[ID]map[ID]bool{},
		traversable: map[ID]map[ID]bool{},
	}
}

// AddObjectType registers an object type.
func (g *Graph) AddObjectType(t ObjectType) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[t.ID]; ok {
		return ErrDuplicate
	}
	g.objectTypes[t.ID] = t
	g.seq++
	return nil
}

// AddLinkType registers a link type.
func (g *Graph) AddLinkType(t LinkType) error {
	if err := ValidateID(t.ID); err != nil {
		return err
	}
	if err := ValidateID(t.FromType); err != nil {
		return err
	}
	if err := ValidateID(t.ToType); err != nil {
		return err
	}
	if t.Direction != Directed && t.Direction != Bidirectional {
		return errors.New("ontology: invalid direction")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[t.ID]; ok {
		return ErrDuplicate
	}
	g.linkTypes[t.ID] = t
	g.seq++
	return nil
}

// AddObject inserts an object instance.
func (g *Graph) AddObject(o ObjectInstance) error {
	if err := ValidateID(o.ID); err != nil {
		return err
	}
	if err := ValidateID(o.ObjectType); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[o.ID]; ok {
		return ErrDuplicate
	}
	if _, ok := g.objectTypes[o.ObjectType]; !ok {
		return ErrTypeMismatch
	}
	g.objects[o.ID] = o
	g.seq++
	return nil
}

// AddLink inserts a link instance and its derived arcs.
func (g *Graph) AddLink(l LinkInstance) error {
	if err := ValidateID(l.ID); err != nil {
		return err
	}
	if err := ValidateID(l.LinkType); err != nil {
		return err
	}
	if err := ValidateID(l.Tail); err != nil {
		return err
	}
	if err := ValidateID(l.Head); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.links[l.ID]; ok {
		return ErrDuplicate
	}
	lt, ok := g.linkTypes[l.LinkType]
	if !ok {
		return ErrTypeMismatch
	}
	tail, okT := g.objects[l.Tail]
	head, okH := g.objects[l.Head]
	if !okT || !okH {
		return ErrNotFound
	}
	if tail.ObjectType != lt.FromType || head.ObjectType != lt.ToType {
		return ErrTypeMismatch
	}
	fwd := arc{linkID: l.ID, lt: l.LinkType, from: l.Tail, to: l.Head}
	g.out[l.Tail] = append(g.out[l.Tail], fwd)
	g.inArcs[l.Head] = append(g.inArcs[l.Head], fwd)
	if lt.Direction == Bidirectional {
		rev := fwd
		rev.rev = true
		rev.from, rev.to = l.Head, l.Tail
		g.out[l.Head] = append(g.out[l.Head], rev)
		g.inArcs[l.Tail] = append(g.inArcs[l.Tail], rev)
	}
	g.links[l.ID] = l
	g.seq++
	return nil
}

// HasObject reports whether id names an existing object instance.
func (g *Graph) HasObject(id ID) bool {
	if err := ValidateID(id); err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.objects[id]
	return ok
}
