package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// adjEntry is one adjacency-list entry: traversal over link `link` leads
// to object `to`. Entries exist only in traversable directions.
type adjEntry struct {
	link LinkID
	to   ObjectID
}

type objectState struct {
	typ      ObjectType
	isolated bool
	adj      []adjEntry // kept sorted by link ID for deterministic scans
}

type linkState struct {
	typ      LinkTypeID
	src, dst ObjectID
}

// Store holds the ontology graph. All mutations take the write lock and
// all queries the read lock, so every operation is linearizable: the
// result of any interleaved execution equals some serial execution, and
// a query always observes one complete snapshot of the graph.
type Store struct {
	mu          sync.RWMutex
	catRank     map[Category]int // category -> fixed order rank
	objectTypes map[ObjectType]bool
	forbidden   map[ObjectType]bool
	linkTypes   map[LinkTypeID]*LinkType
	objects     map[ObjectID]*objectState
	links       map[LinkID]*linkState
}

// NewStore creates a Store with the fixed category set (the slice order
// defines the fixed category ordering used for tie-breaking) and the
// fixed object type set.
func NewStore(categories []Category, objectTypes []ObjectType) (*Store, error) {
	s := &Store{
		catRank:     make(map[Category]int, len(categories)),
		objectTypes: make(map[ObjectType]bool, len(objectTypes)),
		forbidden:   make(map[ObjectType]bool),
		linkTypes:   make(map[LinkTypeID]*LinkType),
		objects:     make(map[ObjectID]*objectState),
		links:       make(map[LinkID]*linkState),
	}
	if len(categories) == 0 {
		return nil, fmt.Errorf("ontology: category set must not be empty")
	}
	for i, c := range categories {
		if _, dup := s.catRank[c]; dup {
			return nil, fmt.Errorf("ontology: duplicate category %q", c)
		}
		s.catRank[c] = i
	}
	for _, t := range objectTypes {
		s.objectTypes[t] = true
	}
	return s, nil
}

// AddObject registers an object instance of a known object type.
func (s *Store) AddObject(id ObjectID, typ ObjectType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.objectTypes[typ] {
		return fmt.Errorf("ontology: unknown object type %q", typ)
	}
	if _, ok := s.objects[id]; ok {
		return fmt.Errorf("ontology: object %q already exists", id)
	}
	s.objects[id] = &objectState{typ: typ}
	return nil
}

// RemoveObject deletes an object and all links incident to it.
func (s *Store) RemoveObject(id ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok {
		return fmt.Errorf("ontology: object %q not found", id)
	}
	victims := make([]LinkID, 0, len(o.adj))
	for _, e := range o.adj {
		victims = append(victims, e.link)
	}
	for _, lid := range victims {
		s.removeLinkLocked(lid)
	}
	delete(s.objects, id)
	return nil
}

// SetIsolation marks or unmarks an object as logically isolated.
func (s *Store) SetIsolation(id ObjectID, isolated bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	if !ok {
		return fmt.Errorf("ontology: object %q not found", id)
	}
	o.isolated = isolated
	return nil
}

// SetTypeForbidden marks an object type as forbidden (or allowed again)
// for path queries.
func (s *Store) SetTypeForbidden(typ ObjectType, forbidden bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.objectTypes[typ] {
		return fmt.Errorf("ontology: unknown object type %q", typ)
	}
	s.forbidden[typ] = forbidden
	return nil
}

// AddLinkType registers a link type. The category and both endpoint
// object types must belong to the fixed sets; cost must be <= MaxLinkCost.
func (s *Store) AddLinkType(lt LinkType) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linkTypes[lt.ID]; ok {
		return fmt.Errorf("ontology: link type %q already exists", lt.ID)
	}
	if _, ok := s.catRank[lt.Category]; !ok {
		return fmt.Errorf("ontology: unknown category %q", lt.Category)
	}
	if !s.objectTypes[lt.SrcType] || !s.objectTypes[lt.DstType] {
		return fmt.Errorf("ontology: link type %q uses unknown object type", lt.ID)
	}
	if lt.Cost > MaxLinkCost {
		return fmt.Errorf("ontology: link type %q cost %d exceeds %d", lt.ID, lt.Cost, MaxLinkCost)
	}
	cp := lt
	if lt.Principals != nil {
		cp.Principals = make(map[Principal]bool, len(lt.Principals))
		for p := range lt.Principals {
			cp.Principals[p] = true
		}
	}
	s.linkTypes[lt.ID] = &cp
	return nil
}

// SetLinkTypePrincipals replaces the visibility set of a link type;
// nil makes the type visible to everyone.
func (s *Store) SetLinkTypePrincipals(id LinkTypeID, principals map[Principal]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTypes[id]
	if !ok {
		return fmt.Errorf("ontology: link type %q not found", id)
	}
	lt.Principals = nil
	if principals != nil {
		lt.Principals = make(map[Principal]bool, len(principals))
		for p := range principals {
			lt.Principals[p] = true
		}
	}
	return nil
}

// AddLink creates a link instance between two existing objects whose
// types must match the endpoint types of the link type.
func (s *Store) AddLink(id LinkID, typ LinkTypeID, src, dst ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[id]; ok {
		return fmt.Errorf("ontology: link %q already exists", id)
	}
	lt, ok := s.linkTypes[typ]
	if !ok {
		return fmt.Errorf("ontology: link type %q not found", typ)
	}
	so, ok := s.objects[src]
	if !ok {
		return fmt.Errorf("ontology: object %q not found", src)
	}
	do, ok := s.objects[dst]
	if !ok {
		return fmt.Errorf("ontology: object %q not found", dst)
	}
	if so.typ != lt.SrcType || do.typ != lt.DstType {
		return fmt.Errorf("ontology: link %q endpoint types do not match link type %q", id, typ)
	}
	s.links[id] = &linkState{typ: typ, src: src, dst: dst}
	so.adj = insertAdj(so.adj, adjEntry{link: id, to: dst})
	if lt.Bidirectional {
		do.adj = insertAdj(do.adj, adjEntry{link: id, to: src})
	}
	return nil
}

// RemoveLink deletes a link instance.
func (s *Store) RemoveLink(id LinkID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[id]; !ok {
		return fmt.Errorf("ontology: link %q not found", id)
	}
	s.removeLinkLocked(id)
	return nil
}

func (s *Store) removeLinkLocked(id LinkID) {
	l := s.links[id]
	lt := s.linkTypes[l.typ]
	if o, ok := s.objects[l.src]; ok {
		o.adj = removeAdj(o.adj, id)
	}
	if lt.Bidirectional {
		if o, ok := s.objects[l.dst]; ok {
			o.adj = removeAdj(o.adj, id)
		}
	}
	delete(s.links, id)
}

func insertAdj(adj []adjEntry, e adjEntry) []adjEntry {
	adj = append(adj, e)
	sort.Slice(adj, func(i, j int) bool { return adj[i].link < adj[j].link })
	return adj
}

func removeAdj(adj []adjEntry, id LinkID) []adjEntry {
	out := adj[:0]
	for _, e := range adj {
		if e.link != id {
			out = append(out, e)
		}
	}
	return out
}
