package ontology

import (
	"errors"
	"sort"
	"sync"
)

// ChangeKind enumerates the kinds of journaled main-graph changes.
type ChangeKind int

const (
	ChangeObjectAdded ChangeKind = iota + 1
	ChangeObjectRemoved
	ChangeLinkAdded
	ChangeLinkRemoved
	ChangePermissionGranted
	ChangePermissionRevoked
)

// Change is one journaled, revision-ordered mutation of the main graph.
// Journal entries are the authoritative basis for attributing differences
// between two snapshots extracted at different revisions.
type Change struct {
	Revision  int64
	Kind      ChangeKind
	Principal string // permission changes only
	Object    ID     // object or permission changes
	Link      ID     // link changes only
}

// graphState is one immutable point-in-time state of the main graph.
// Readers obtain a pointer once and never observe a mixed state across
// objects and links, because writers always publish a fresh cloned state.
type graphState struct {
	revision int64
	objects  map[ID]Object
	links    map[ID]Link
	// endpointLinks indexes link IDs under each endpoint they touch.
	// A link is indexed once per distinct endpoint, so a self link is
	// indexed exactly once. Extraction therefore visits each candidate
	// link once and never walks links unrelated to the scope.
	endpointLinks map[ID]map[ID]struct{}
	grants        map[string]map[ID]struct{} // principal -> object set
	canExist      map[ID]map[string]struct{} // object -> principal set
	journal       []Change
}

// Graph is the versioned main link graph. All mutations are serialized;
// each committed mutation produces exactly one new immutable revision.
// Readers are lock-free relative to writers.
type Graph struct {
	mu    sync.Mutex
	state *graphState
}

// NewGraph creates an empty main graph.
func NewGraph() *Graph {
	return &Graph{state: &graphState{
		objects:       map[ID]Object{},
		links:         map[ID]Link{},
		endpointLinks: map[ID]map[ID]struct{}{},
		grants:        map[string]map[ID]struct{}{},
		canExist:      map[ID]map[ID]struct{}{},
	}}
}

var (
	// ErrInvalidArgument is returned when a mutation references malformed data.
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrNotFound is returned when a referenced entity is absent.
	ErrNotFound = errors.New("ontology: not found")
	// ErrConflict is returned when a created entity already exists.
	ErrConflict = errors.New("ontology: already exists")
	// ErrDanglingEndpoint is returned when a link references a missing object.
	ErrDanglingEndpoint = errors.New("ontology: link endpoint object not found")
)

// snapshotState atomically returns the current immutable state pointer.
func (g *Graph) snapshotState() *graphState {
	g.mu.Lock()
	s := g.state
	g.mu.Unlock()
	return s
}

// Revision returns the current main-graph revision number.
func (g *Graph) Revision() int64 {
	return g.snapshotState().revision
}

// cloneForWrite returns a private, mutable deep copy of the current state
// and must be called with g.mu held.
func (g *Graph) cloneForWrite() *graphState {
	cur := g.state
	next := &graphState{
		revision:      cur.revision,
		objects:       make(map[ID]Object, len(cur.objects)),
		links:         make(map[ID]Link, len(cur.links)),
		endpointLinks: make(map[ID]map[ID]struct{}, len(cur.endpointLinks)),
		grants:        make(map[string]map[ID]struct{}, len(cur.grants)),
		canExist:      make(map[ID]map[ID]struct{}, len(cur.canExist)),
		journal:       append([]Change(nil), cur.journal...),
	}
	for k, v := range cur.objects {
		next.objects[k] = v
	}
	for k, v := range cur.links {
		next.links[k] = v
	}
	for obj, linkIDs := range cur.endpointLinks {
		cp := make(map[ID]struct{}, len(linkIDs))
		for id := range linkIDs {
			cp[id] = struct{}{}
		}
		next.endpointLinks[obj] = cp
	}
	for principal, objs := range cur.grants {
		cp := make(map[ID]struct{}, len(objs))
		for o := range objs {
			cp[o] = struct{}{}
		}
		next.grants[principal] = cp
	}
	for obj, principals := range cur.canExist {
		cp := make(map[string]struct{}, len(principals))
		for p := range principals {
			cp[p] = struct{}{}
		}
		next.canExist[obj] = cp
	}
	return next
}

// appendJournal records one journal entry and bumps the revision.
// Must be called with g.mu held.
func (s *graphState) appendJournal(kind ChangeKind, ch Change) {
	s.revision++
	ch.Revision = s.revision
	ch.Kind = kind
	s.journal = append(s.journal, ch)
}

// mut runs fn against a private cloned state; on nil error the state and
// one journal entry are committed atomically, otherwise the main graph is
// left untouched.
func (g *Graph) mut(kind ChangeKind, fn func(next *graphState) (Change, error)) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := g.cloneForWrite()
	ch, err := fn(next)
	if err != nil {
		return g.state.revision, err
	}
	next.appendJournal(kind, ch)
	g.state = next
	return next.revision, nil
}

func addEndpointLink(m map[ID]map[ID]struct{}, endpoint, linkID ID) {
	set, ok := m[endpoint]
	if !ok {
		set = map[ID]struct{}{}
		m[endpoint] = set
	}
	set[linkID] = struct{}{}
}

func removeEndpointLink(m map[ID]map[ID]struct{}, endpoint, linkID ID) {
	if set, ok := m[endpoint]; ok {
		delete(set, linkID)
		if len(set) == 0 {
			delete(m, endpoint)
		}
	}
}

// indexLink records lk under each distinct endpoint it touches.
func indexLink(m map[ID]map[ID]struct{}, lk Link) {
	addEndpointLink(m, lk.From, lk.ID)
	if lk.To != lk.From {
		addEndpointLink(m, lk.To, lk.ID)
	}
}

func unindexLink(m map[ID]map[ID]struct{}, lk Link) {
	removeEndpointLink(m, lk.From, lk.ID)
	if lk.To != lk.From {
		removeEndpointLink(m, lk.To, lk.ID)
	}
}

// AddObject adds a new object.
func (g *Graph) AddObject(obj Object) (int64, error) {
	if !obj.Valid() {
		return g.Revision(), ErrInvalidArgument
	}
	return g.mut(ChangeObjectAdded, func(next *graphState) (Change, error) {
		if _, exists := next.objects[obj.ID]; exists {
			return Change{}, ErrConflict
		}
		next.objects[obj.ID] = obj
		return Change{Object: obj.ID}, nil
	})
}

// RemoveObject removes an object together with every incident link and all
// of its existence grants. The cascading removals are journaled individually
// so cross-revision differences stay fully attributable.
func (g *Graph) RemoveObject(id ID) (int64, error) {
	if !ValidID(id) {
		return g.Revision(), ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.state.objects[id]; !ok {
		return g.state.revision, ErrNotFound
	}
	next := g.cloneForWrite()
	incident := make([]Link, 0)
	for _, lk := range next.links {
		if lk.From == id || lk.To == id {
			incident = append(incident, lk)
		}
	}
	sort.Slice(incident, func(i, j int) bool { return incident[i].ID < incident[j].ID })
	for _, lk := range incident {
		unindexLink(next.endpointLinks, lk)
		delete(next.links, lk.ID)
		next.appendJournal(ChangeLinkRemoved, Change{Link: lk.ID})
	}
	if principals, ok := next.canExist[id]; ok {
		ps := make([]string, 0, len(principals))
		for p := range principals {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		for _, p := range ps {
			if set, ok := next.grants[p]; ok {
				delete(set, id)
				if len(set) == 0 {
					delete(next.grants, p)
				}
			}
			next.appendJournal(ChangePermissionRevoked, Change{
				Principal: p, Object: id,
			})
		}
		delete(next.canExist, id)
	}
	delete(next.objects, id)
	delete(next.endpointLinks, id)
	next.appendJournal(ChangeObjectRemoved, Change{Object: id})
	g.state = next
	return next.revision, nil
}

// AddLink adds a new link; both endpoint objects must already exist so the
// main graph never stores a link with a missing endpoint.
func (g *Graph) AddLink(lk Link) (int64, error) {
	if !lk.Valid() {
		return g.Revision(), ErrInvalidArgument
	}
	return g.mut(ChangeLinkAdded, func(next *graphState) (Change, error) {
		if _, exists := next.links[lk.ID]; exists {
			return Change{}, ErrConflict
		}
		if _, ok := next.objects[lk.From]; !ok {
			return Change{}, ErrDanglingEndpoint
		}
		if _, ok := next.objects[lk.To]; !ok {
			return Change{}, ErrDanglingEndpoint
		}
		next.links[lk.ID] = lk
		indexLink(next.endpointLinks, lk)
		return Change{Link: lk.ID}, nil
	})
}

// RemoveLink removes an existing link.
func (g *Graph) RemoveLink(id ID) (int64, error) {
	if !ValidID(id) {
		return g.Revision(), ErrInvalidArgument
	}
	return g.mut(ChangeLinkRemoved, func(next *graphState) (Change, error) {
		lk, ok := next.links[id]
		if !ok {
			return Change{}, ErrNotFound
		}
		unindexLink(next.endpointLinks, lk)
		delete(next.links, id)
		return Change{Link: id}, nil
	})
}

// GrantExistence grants a principal the existence permission on an object.
func (g *Graph) GrantExistence(principal string, object ID) (int64, error) {
	if principal == "" || !ValidID(object) {
		return g.Revision(), ErrInvalidArgument
	}
	return g.mut(ChangePermissionGranted, func(next *graphState) (Change, error) {
		if _, ok := next.objects[object]; !ok {
			return Change{}, ErrNotFound
		}
		set, ok := next.grants[principal]
		if !ok {
			set = map[ID]struct{}{}
			next.grants[principal] = set
		}
		set[object] = struct{}{}
		pset, ok := next.canExist[object]
		if !ok {
			pset = map[string]struct{}{}
			next.canExist[object] = pset
		}
		pset[principal] = struct{}{}
		return Change{Principal: principal, Object: object}, nil
	})
}

// RevokeExistence revokes the existence permission.
func (g *Graph) RevokeExistence(principal string, object ID) (int64, error) {
	if principal == "" || !ValidID(object) {
		return g.Revision(), ErrInvalidArgument
	}
	return g.mut(ChangePermissionRevoked, func(next *graphState) (Change, error) {
		set, ok := next.grants[principal]
		if !ok {
			return Change{}, ErrNotFound
		}
		if _, ok := set[object]; !ok {
			return Change{}, ErrNotFound
		}
		delete(set, object)
		if len(set) == 0 {
			delete(next.grants, principal)
		}
		if pset, ok := next.canExist[object]; ok {
			delete(pset, principal)
			if len(pset) == 0 {
				delete(next.canExist, object)
			}
		}
		return Change{Principal: principal, Object: object}, nil
	})
}

// Journal returns a copy of the change entries with revision in (after, now].
func (g *Graph) Journal(after, now int64) []Change {
	s := g.snapshotState()
	out := make([]Change, 0)
	for _, ch := range s.journal {
		if ch.Revision > after && ch.Revision <= now {
			out = append(out, ch)
		}
	}
	return out
}
