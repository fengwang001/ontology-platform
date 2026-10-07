package compensate

import "sort"

// InstanceID identifies an object instance.
type InstanceID string

// LinkID identifies a link instance.
type LinkID string

// Attrs is a property map of an object instance.
type Attrs map[string]any

// Object is one object instance with observable properties and version.
type Object struct {
	ID      InstanceID
	Attrs   Attrs
	Version int64
}

// Link is one link instance.
type Link struct {
	ID     LinkID
	Source InstanceID
	Target InstanceID
	Type   string
}

// Graph is the mutable object graph operated on by actions.
type Graph struct {
	mu     chan struct{} // serializes structural mutation + lock table access
	objs   map[InstanceID]*Object
	links  map[LinkID]*Link
	locked map[string]bool

	// polluted maps instance -> the earliest failing op index across every
	// compensation the instance has ever been involved in. The first write
	// wins; later contaminations never overwrite it.
	polluted map[InstanceID]int
}

func tokenLock(k string) string { return "L:" + k }
func tokenObj(id InstanceID) string {
	return "O:" + string(id)
}
func tokenLink(id LinkID) string { return "K:" + string(id) }

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		mu:       make(chan struct{}, 1),
		objs:     map[InstanceID]*Object{},
		links:    map[LinkID]*Link{},
		locked:   map[string]bool{},
		polluted: map[InstanceID]int{},
	}
}

func (g *Graph) lock()   { g.mu <- struct{}{} }
func (g *Graph) unlock() { <-g.mu }

// AddObject inserts an object instance.
func (g *Graph) AddObject(o Object) {
	g.lock()
	defer g.unlock()
	cp := make(Attrs, len(o.Attrs))
	for k, v := range o.Attrs {
		cp[k] = v
	}
	g.objs[o.ID] = &Object{ID: o.ID, Attrs: cp, Version: o.Version}
}

// AddLink inserts a link instance.
func (g *Graph) AddLink(l Link) {
	g.lock()
	defer g.unlock()
	g.links[l.ID] = &l
}

// SnapshotAttrs returns a defensive copy of an instance's attributes.
func (g *Graph) SnapshotAttrs(id InstanceID) Attrs {
	g.lock()
	defer g.unlock()
	o, ok := g.objs[id]
	if !ok {
		return nil
	}
	cp := make(Attrs, len(o.Attrs))
	for k, v := range o.Attrs {
		cp[k] = v
	}
	return cp
}

// SnapshotVersion returns the version stamp of an instance.
func (g *Graph) SnapshotVersion(id InstanceID) int64 {
	g.lock()
	defer g.unlock()
	if o, ok := g.objs[id]; ok {
		return o.Version
	}
	return 0
}

// HasLink reports whether a link currently exists.
func (g *Graph) HasLink(id LinkID) bool {
	g.lock()
	defer g.unlock()
	_, ok := g.links[id]
	return ok
}

// Link returns a copy of a link instance.
func (g *Graph) Link(id LinkID) (Link, bool) {
	g.lock()
	defer g.unlock()
	l, ok := g.links[id]
	if !ok {
		return Link{}, false
	}
	return *l, true
}

// tryAcquire takes every listed resource token atomically, in global sorted
// order. Either all tokens are acquired or none (rejection before any change).
func (g *Graph) tryAcquire(tokens []string) bool {
	sort.Strings(tokens)
	g.lock()
	for _, t := range tokens {
		if g.locked[t] {
			g.unlock()
			return false
		}
	}
	for _, t := range tokens {
		g.locked[t] = true
	}
	g.unlock()
	return true
}

func (g *Graph) release(tokens []string) {
	sort.Strings(tokens)
	g.lock()
	for _, t := range tokens {
		delete(g.locked, t)
	}
	g.unlock()
}

// earliestPollution returns the first recorded failing op index for id.
func (g *Graph) earliestPollution(id InstanceID) (int, bool) {
	g.lock()
	defer g.unlock()
	idx, ok := g.polluted[id]
	return idx, ok
}

// markPolluted records contamination, keeping the earliest index forever.
func (g *Graph) markPolluted(id InstanceID, idx int) {
	g.lock()
	defer g.unlock()
	if cur, ok := g.polluted[id]; !ok || idx < cur {
		g.polluted[id] = idx
	}
}

// Repair clears the pollution marker after explicit manual repair.
func (g *Graph) Repair(id InstanceID) {
	g.lock()
	defer g.unlock()
	delete(g.polluted, id)
}

// IsPolluted reports the contamination state of an instance.
func (g *Graph) IsPolluted(id InstanceID) bool {
	_, ok := g.earliestPollution(id)
	return ok
}

// PollutionIndex returns the earliest failing op index recorded for id.
func (g *Graph) PollutionIndex(id InstanceID) (int, bool) {
	return g.earliestPollution(id)
}

// Atomic attribute mutation primitives. Each primitive is an indivisible
// check-then-commit: validation and the actual mutation happen inside one
// critical section, so an inverse that fails its precondition never performs
// a partial mutation. The same critical section is the caller's
// "effect + inverse registration" processing unit.

func (g *Graph) setAttrsIfExists(id InstanceID, attrs Attrs) (Attrs, int64, error) {
	g.lock()
	defer g.unlock()
	o, ok := g.objs[id]
	if !ok {
		return nil, 0, &BusinessError{Msg: "object not found: " + string(id)}
	}
	old := make(Attrs, len(o.Attrs))
	for k, v := range o.Attrs {
		old[k] = v
	}
	if o.Attrs == nil {
		o.Attrs = Attrs{}
	}
	for k, v := range attrs {
		o.Attrs[k] = v
	}
	o.Version++
	return old, o.Version - 1, nil
}

func (g *Graph) restoreAttrs(id InstanceID, old Attrs, oldVersion int64) error {
	g.lock()
	defer g.unlock()
	o, ok := g.objs[id]
	if !ok {
		return &BusinessError{Msg: "inverse: object not found: " + string(id)}
	}
	cp := make(Attrs, len(old))
	for k, v := range old {
		cp[k] = v
	}
	o.Attrs = cp
	o.Version = oldVersion
	return nil
}

func (g *Graph) createLinkIfAbsent(l Link) error {
	g.lock()
	defer g.unlock()
	if _, ok := g.links[l.ID]; ok {
		return &BusinessError{Msg: "link already exists: " + string(l.ID)}
	}
	lc := l
	g.links[l.ID] = &lc
	return nil
}

func (g *Graph) deleteLinkIfExists(id LinkID) (Link, error) {
	g.lock()
	defer g.unlock()
	l, ok := g.links[id]
	if !ok {
		return Link{}, &BusinessError{Msg: "link not found: " + string(id)}
	}
	old := *l
	delete(g.links, id)
	return old, nil
}

func (g *Graph) restoreDeletedLink(l Link) error {
	g.lock()
	defer g.unlock()
	if _, ok := g.links[l.ID]; ok {
		return &BusinessError{Msg: "inverse: link id reoccupied: " + string(l.ID)}
	}
	lc := l
	g.links[l.ID] = &lc
	return nil
}

func (g *Graph) removeCreatedLink(id LinkID) error {
	g.lock()
	defer g.unlock()
	if _, ok := g.links[id]; !ok {
		return &BusinessError{Msg: "inverse: link already gone: " + string(id)}
	}
	delete(g.links, id)
	return nil
}

func (g *Graph) objectExists(id InstanceID) bool {
	g.lock()
	defer g.unlock()
	_, ok := g.objs[id]
	return ok
}
