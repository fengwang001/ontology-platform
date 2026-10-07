package ontology

import (
	"context"
	"fmt"
	"regexp"
	"sync"
)

// edge is one directed, traversable-in-this-direction adjacency entry.
type edge struct {
	linkType string
	to       string
}

// snap is an immutable point-in-time view of the whole graph and of every
// caller's permission set. ReachableFrom acquires exactly one snap at entry;
// concurrent grants/revokes never affect an in-flight query, which makes
// every history of operations equivalent to some serial order.
type snap struct {
	epoch int64

	objectTypes map[string]struct{}
	linkTypes   map[string]Direction
	objects     map[string]Object
	links       map[string]Link

	// adjacency is pre-expanded per direction so the search never has to
	// consult link-type direction while walking.
	adj map[string][]edge

	// existence[c][objectID]; traversal[c][linkType]. Absence means deny.
	existence map[CallerID]map[string]struct{}
	traversal map[CallerID]map[string]struct{}
}

func newSnap() *snap {
	return &snap{
		objectTypes: map[string]struct{}{},
		linkTypes:   map[string]Direction{},
		objects:     map[string]Object{},
		links:       map[string]Link{},
		adj:         map[string][]edge{},
		existence:   map[CallerID]map[string]struct{}{},
		traversal:   map[CallerID]map[string]struct{}{},
	}
}

func (s *snap) visible(caller CallerID, id string) bool {
	perms, ok := s.existence[caller]
	if !ok {
		return false
	}
	_, ok = perms[id]
	return ok
}

func (s *snap) canTraverse(caller CallerID, linkType string) bool {
	perms, ok := s.traversal[caller]
	if !ok {
		return false
	}
	_, ok = perms[linkType]
	return ok
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_.\-:]{1,128}$`)

func validIdentifier(id string) bool { return validID.MatchString(id) }

// QueryError reports a definite, permission-independent failure (bad
// parameter or genuine absence). It is never returned for mere lack of
// visibility.
type QueryError struct {
	Kind   string
	ID     string
	Caller CallerID
}

func (e *QueryError) Error() string { return e.Kind + ": " + e.ID }

func ErrInvalidID(id string) *QueryError    { return &QueryError{Kind: "invalid_id", ID: id} }
func ErrMissingStart(id string) *QueryError { return &QueryError{Kind: "missing_start", ID: id} }
func ErrMissingEnd(id string) *QueryError   { return &QueryError{Kind: "missing_end", ID: id} }

// Metrics counts work actually attempted during one query. Counters are
// internal-only diagnostics; callers never receive them in API results.
type Metrics struct {
	ObjectsDequeued       int
	LinksInspected        int
	LinksBlocked          int
	ObjectsInvisible      int
	ShadowObjectsDequeued int
	ShadowLinksInspected  int
}

// Tracer receives internal query diagnostics for verification. Production
// callers leave it nil; it must never influence query results.
type Tracer interface {
	Trace(ev map[string]any)
}

// Graph is the concurrent-safe ontology store.
//
// Mutations are serialized; reads acquire an atomic pointer to an immutable
// snapshot (copy-on-write maps). A query's linearization point is the
// snapshot acquisition, so grants/revokes accepted before it are visible and
// ones accepted after it are not, exactly as in a serial execution.
type Graph struct {
	mu  sync.RWMutex
	cur *snap
}

func NewGraph() *Graph {
	return &Graph{cur: newSnap()}
}

func (g *Graph) snapshot() *snap {
	g.mu.RLock()
	s := g.cur
	g.mu.RUnlock()
	return s
}

// mutate clones the current snapshot, applies fn, then atomically publishes
// it. Only map headers that fn touches must be copied; fn must do that
// copying itself via the clone helpers below.
func (g *Graph) mutate(fn func(s *snap) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.cur
	clone := *s
	clone.epoch++
	if err := fn(&clone); err != nil {
		return err
	}
	g.cur = &clone
	return nil
}

func (g *Graph) AddObjectType(name string) error {
	if !validIdentifier(name) {
		return fmt.Errorf("ontology: invalid object type name %q", name)
	}
	return g.mutate(func(s *snap) error {
		if _, exists := s.objectTypes[name]; exists {
			return fmt.Errorf("ontology: object type %q already exists", name)
		}
		m := make(map[string]struct{}, len(s.objectTypes)+1)
		for k := range s.objectTypes {
			m[k] = struct{}{}
		}
		m[name] = struct{}{}
		s.objectTypes = m
		return nil
	})
}

func (g *Graph) AddLinkType(name string, dir Direction) error {
	if !validIdentifier(name) {
		return fmt.Errorf("ontology: invalid link type name %q", name)
	}
	if !dir.Valid() {
		return fmt.Errorf("ontology: invalid direction for link type %q", name)
	}
	return g.mutate(func(s *snap) error {
		if _, exists := s.linkTypes[name]; exists {
			return fmt.Errorf("ontology: link type %q already exists", name)
		}
		m := make(map[string]Direction, len(s.linkTypes)+1)
		for k, v := range s.linkTypes {
			m[k] = v
		}
		m[name] = dir
		s.linkTypes = m
		return nil
	})
}

func (g *Graph) AddObject(o Object) error {
	if !validIdentifier(o.ID) {
		return fmt.Errorf("ontology: invalid object id %q", o.ID)
	}
	if !validIdentifier(o.ObjectType) {
		return fmt.Errorf("ontology: invalid object type %q", o.ObjectType)
	}
	return g.mutate(func(s *snap) error {
		if _, exists := s.objects[o.ID]; exists {
			return fmt.Errorf("ontology: object %q already exists", o.ID)
		}
		if _, ok := s.objectTypes[o.ObjectType]; !ok {
			return fmt.Errorf("ontology: unknown object type %q", o.ObjectType)
		}
		m := make(map[string]Object, len(s.objects)+1)
		for k, v := range s.objects {
			m[k] = v
		}
		m[o.ID] = o
		s.objects = m
		return nil
	})
}

func (g *Graph) AddLink(l Link) error {
	if !validIdentifier(l.ID) || !validIdentifier(l.LinkType) ||
		!validIdentifier(l.Src) || !validIdentifier(l.Dst) {
		return fmt.Errorf("ontology: invalid link %v", l)
	}
	return g.mutate(func(s *snap) error {
		if _, exists := s.links[l.ID]; exists {
			return fmt.Errorf("ontology: link %q already exists", l.ID)
		}
		dir, ok := s.linkTypes[l.LinkType]
		if !ok {
			return fmt.Errorf("ontology: unknown link type %q", l.LinkType)
		}
		if _, ok := s.objects[l.Src]; !ok {
			return fmt.Errorf("ontology: unknown source object %q", l.Src)
		}
		if _, ok := s.objects[l.Dst]; !ok {
			return fmt.Errorf("ontology: unknown destination object %q", l.Dst)
		}

		links := make(map[string]Link, len(s.links)+1)
		for k, v := range s.links {
			links[k] = v
		}
		links[l.ID] = l
		s.links = links

		adj := make(map[string][]edge, len(s.adj)+1)
		for k, v := range s.adj {
			adj[k] = v
		}
		adj[l.Src] = append(append([]edge(nil), adj[l.Src]...), edge{linkType: l.LinkType, to: l.Dst})
		if dir == Bidirectional {
			adj[l.Dst] = append(append([]edge(nil), adj[l.Dst]...), edge{linkType: l.LinkType, to: l.Src})
		}
		s.adj = adj
		return nil
	})
}

func (g *Graph) setExistence(c CallerID, objectID string, grant bool) {
	_ = g.mutate(func(s *snap) error {
		perms := make(map[string]struct{}, len(s.existence[c])+1)
		for k := range s.existence[c] {
			perms[k] = struct{}{}
		}
		if grant {
			perms[objectID] = struct{}{}
		} else {
			delete(perms, objectID)
		}
		all := make(map[CallerID]map[string]struct{}, len(s.existence)+1)
		for k, v := range s.existence {
			all[k] = v
		}
		all[c] = perms
		s.existence = all
		return nil
	})
}

func (g *Graph) setTraversal(c CallerID, linkType string, grant bool) {
	_ = g.mutate(func(s *snap) error {
		perms := make(map[string]struct{}, len(s.traversal[c])+1)
		for k := range s.traversal[c] {
			perms[k] = struct{}{}
		}
		if grant {
			perms[linkType] = struct{}{}
		} else {
			delete(perms, linkType)
		}
		all := make(map[CallerID]map[string]struct{}, len(s.traversal)+1)
		for k, v := range s.traversal {
			all[k] = v
		}
		all[c] = perms
		s.traversal = all
		return nil
	})
}

func (g *Graph) GrantExistence(c CallerID, objectID string)  { g.setExistence(c, objectID, true) }
func (g *Graph) RevokeExistence(c CallerID, objectID string) { g.setExistence(c, objectID, false) }
func (g *Graph) GrantTraversal(c CallerID, linkType string)  { g.setTraversal(c, linkType, true) }
func (g *Graph) RevokeTraversal(c CallerID, linkType string) { g.setTraversal(c, linkType, false) }

func (g *Graph) ReachableFrom(ctx context.Context, start, end string, caller CallerID) (Outcome, error) {
	out, _, _, err := g.ReachableFromTraced(ctx, start, end, caller, nil)
	return out, err
}

func (g *Graph) ReachableFromTraced(ctx context.Context, start, end string, caller CallerID, tracer Tracer) (Outcome, Reason, *Metrics, error) {
	s := g.snapshot()
	return reachableFrom(ctx, s, start, end, caller, tracer)
}
