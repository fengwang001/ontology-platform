package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// state holds all mutable data. Mutations happen only under Store.mu write
// lock, so the forward and reverse indexes are always updated atomically.
type state struct {
	objectTypes map[string]bool
	linkTypes   map[string]*LinkType
	objects     map[string]string // objectID -> objectType name
	// fwd[linkType][sourceID] = set of targetIDs
	fwd map[string]map[string]map[string]bool
	// rev[linkType][targetID] = set of sourceIDs (exact mirror of fwd)
	rev map[string]map[string]map[string]bool
}

func newState() *state {
	return &state{
		objectTypes: make(map[string]bool),
		linkTypes:   make(map[string]*LinkType),
		objects:     make(map[string]string),
		fwd:         make(map[string]map[string]map[string]bool),
		rev:         make(map[string]map[string]map[string]bool),
	}
}

// clone deep-copies everything mutable so batch work can be discarded on
// failure without touching the live state.
func (s *state) clone() *state {
	c := newState()
	for k, v := range s.objectTypes {
		c.objectTypes[k] = v
	}
	for k, v := range s.linkTypes {
		c.linkTypes[k] = v // LinkType values are immutable after registration
	}
	for k, v := range s.objects {
		c.objects[k] = v
	}
	for lt, m := range s.fwd {
		c.fwd[lt] = cloneAdjacency(m)
	}
	for lt, m := range s.rev {
		c.rev[lt] = cloneAdjacency(m)
	}
	return c
}

func cloneAdjacency(m map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(m))
	for k, set := range m {
		cp := make(map[string]bool, len(set))
		for v := range set {
			cp[v] = true
		}
		out[k] = cp
	}
	return out
}

// Store is the in-memory ontology graph. All methods are safe for concurrent
// use; every read observes mirrored forward/reverse indexes.
type Store struct {
	mu sync.RWMutex
	st *state
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{st: newState()}
}

// RegisterObjectType declares an object type. Registering the same name
// twice is an error.
func (s *Store) RegisterObjectType(name string) error {
	if name == "" {
		return errors.New("object type name must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.objectTypes[name] {
		return fmt.Errorf("object type %q already registered", name)
	}
	s.st.objectTypes[name] = true
	return nil
}

// RegisterLinkType declares a link type between two registered object types.
func (s *Store) RegisterLinkType(lt LinkType) error {
	if lt.Name == "" {
		return errors.New("link type name must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.st.linkTypes[lt.Name]; dup {
		return fmt.Errorf("link type %q already registered", lt.Name)
	}
	if !s.st.objectTypes[lt.Source] {
		return fmt.Errorf("link type %q: unknown source object type %q", lt.Name, lt.Source)
	}
	if !s.st.objectTypes[lt.Target] {
		return fmt.Errorf("link type %q: unknown target object type %q", lt.Name, lt.Target)
	}
	cp := lt
	s.st.linkTypes[lt.Name] = &cp
	s.st.fwd[lt.Name] = make(map[string]map[string]bool)
	s.st.rev[lt.Name] = make(map[string]map[string]bool)
	return nil
}

// LinkTypeOf returns the declared link type, if any.
func (s *Store) LinkTypeOf(name string) (LinkType, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lt, ok := s.st.linkTypes[name]
	if !ok {
		return LinkType{}, false
	}
	return *lt, true
}

// AddObject creates an object of a registered type with a unique id.
func (s *Store) AddObject(objectType, id string) error {
	if id == "" {
		return errors.New("object id must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.st.objectTypes[objectType] {
		return fmt.Errorf("unknown object type %q", objectType)
	}
	if _, dup := s.st.objects[id]; dup {
		return fmt.Errorf("object %q already exists", id)
	}
	s.st.objects[id] = objectType
	return nil
}

// HasObject reports whether the object exists.
func (s *Store) HasObject(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.st.objects[id]
	return ok
}

// ObjectTypeOf returns the type of an existing object.
func (s *Store) ObjectTypeOf(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.st.objects[id]
	return t, ok
}

// ObjectsOfType lists ids of the given object type, sorted.
func (s *Store) ObjectsOfType(objectType string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return objectsOfType(s.st, objectType)
}

func objectsOfType(st *state, objectType string) []string {
	var out []string
	for id, t := range st.objects {
		if t == objectType {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func sortedLinkTypeNames(st *state) []string {
	names := make([]string, 0, len(st.linkTypes))
	for n := range st.linkTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
