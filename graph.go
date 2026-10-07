package ontology

import (
	"sort"
	"strings"
	"sync"
)

// object is an ontology object instance.
type object struct {
	id     string
	typeID string
}

// link is a link instance between two objects.
type link struct {
	id        string
	typeID    string
	sourceID  string
	targetID  string
	direction Direction
}

// callerView holds the per-caller effective (existence-filtered, then
// traversal-filtered) subgraph. It is maintained incrementally under the
// Graph mutex so that cycle detection never scans objects or links that are
// invisible to the caller.
type callerView struct {
	// existenceGranted is the set of objects the caller may see.
	existenceGranted map[string]bool
	// traversalGranted is the set of links the caller may traverse.
	traversalGranted map[string]bool
	// visibleObjects are objects currently passing the existence filter.
	visibleObjects map[string]bool
	// activeLinks are links whose endpoints are visible and for which the
	// caller holds traversal permission: exactly the edges of the effective
	// subgraph.
	activeLinks map[string]bool
	// outArcs maps a visible object to the active links leaving it,
	// including the reverse arc of bidirectional links.
	outArcs map[string]map[string]bool
}

func newCallerView() *callerView {
	return &callerView{
		existenceGranted: map[string]bool{},
		traversalGranted: map[string]bool{},
		visibleObjects:   map[string]bool{},
		activeLinks:      map[string]bool{},
		outArcs:          map[string]map[string]bool{},
	}
}

// Graph is the ontology graph with an embedded permission model.
//
// All exported operations are safe for concurrent use. Mutations and reads are
// serializable: each HasCycle call observes one immutable snapshot of the
// effective subgraph under the graph mutex.
type Graph struct {
	mu          sync.RWMutex
	objectTypes map[string]ObjectType
	linkTypes   map[string]LinkType
	objects     map[string]*object
	links       map[string]*link
	callers     map[string]*callerView

	// incidentLinks lists every link incident to an object, by link id.
	incidentLinks map[string]map[string]bool

	auditMu     sync.Mutex
	auditWriter func(CallRecord)
	auditLog    []CallRecord
}

func NewGraph() *Graph {
	return &Graph{
		objectTypes:   map[string]ObjectType{},
		linkTypes:     map[string]LinkType{},
		objects:       map[string]*object{},
		links:         map[string]*link{},
		callers:       map[string]*callerView{},
		incidentLinks: map[string]map[string]bool{},
	}
}

// validCallerID enforces non-empty identifiers without surrounding whitespace.
func validCallerID(caller string) bool {
	return caller != "" && caller == strings.TrimSpace(caller)
}

func validID(id string) bool { return validCallerID(id) }

func (g *Graph) view(caller string) *callerView {
	v := g.callers[caller]
	if v == nil {
		v = newCallerView()
		g.callers[caller] = v
	}
	return v
}

func (g *Graph) AddObjectType(id, name string) error {
	if !validID(id) {
		return ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[id]; ok {
		return ErrDuplicate
	}
	g.objectTypes[id] = ObjectType{ID: id, Name: name}
	return nil
}

func (g *Graph) AddLinkType(lt LinkType) error {
	if !validID(lt.ID) {
		return ErrInvalidArgument
	}
	if lt.Direction != Directed && lt.Direction != Bidirectional {
		return ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[lt.ID]; ok {
		return ErrDuplicate
	}
	lt.endpointTyped = lt.SourceTypeID != "" || lt.TargetTypeID != ""
	if lt.SourceTypeID == "" {
		lt.TargetTypeID = ""
	} else if lt.TargetTypeID == "" {
		lt.TargetTypeID = lt.SourceTypeID
	}
	g.linkTypes[lt.ID] = lt
	return nil
}

// AddObject creates an object. The creating caller is granted existence
// permission for it.
func (g *Graph) AddObject(caller, id, typeID string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	if !validID(id) || !validID(typeID) {
		return ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[id]; ok {
		return ErrDuplicate
	}
	if _, ok := g.objectTypes[typeID]; !ok {
		return ErrNotFound
	}
	g.objects[id] = &object{id: id, typeID: typeID}
	g.incidentLinks[id] = map[string]bool{}
	g.grantExistenceLocked(caller, id)
	return nil
}

// AddLink creates a link between two existing objects. The creating caller is
// granted traversal permission for it; whether it enters the caller's
// effective subgraph also depends on endpoint visibility.
func (g *Graph) AddLink(caller, id, linkTypeID, sourceID, targetID string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	if !validID(id) || !validID(linkTypeID) || !validID(sourceID) || !validID(targetID) {
		return ErrInvalidArgument
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.links[id]; ok {
		return ErrDuplicate
	}
	lt, ok := g.linkTypes[linkTypeID]
	if !ok {
		return ErrNotFound
	}
	src, ok := g.objects[sourceID]
	if !ok {
		return ErrNotFound
	}
	dst, ok := g.objects[targetID]
	if !ok {
		return ErrNotFound
	}
	if sourceID == targetID && !lt.AllowSelfLoop {
		return ErrInvalidArgument
	}
	if !lt.AllowParallel {
		for lid := range g.incidentLinks[sourceID] {
			existing := g.links[lid]
			if existing.typeID == linkTypeID &&
				existing.sourceID == sourceID && existing.targetID == targetID {
				return ErrInvalidArgument
			}
		}
	}
	if lt.endpointTyped {
		if src.typeID != lt.SourceTypeID || dst.typeID != lt.TargetTypeID {
			return ErrInvalidArgument
		}
	}
	l := &link{
		id:        id,
		typeID:    linkTypeID,
		sourceID:  sourceID,
		targetID:  targetID,
		direction: lt.Direction,
	}
	g.links[id] = l
	g.incidentLinks[sourceID][id] = true
	g.incidentLinks[targetID][id] = true
	g.grantTraversalLocked(caller, id)
	return nil
}

func (g *Graph) RemoveObject(caller, id string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	obj, ok := g.objects[id]
	if !ok {
		return ErrNotFound
	}
	_ = obj
	for lid := range g.incidentLinks[id] {
		g.removeLinkLocked(lid)
	}
	delete(g.incidentLinks, id)
	for _, v := range g.callers {
		delete(v.existenceGranted, id)
		delete(v.visibleObjects, id)
		delete(v.outArcs, id)
	}
	delete(g.objects, id)
	return nil
}

func (g *Graph) RemoveLink(caller, id string) error {
	if !validCallerID(caller) {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.links[id]; !ok {
		return ErrNotFound
	}
	g.removeLinkLocked(id)
	return nil
}

// removeLinkLocked purges the link from global storage and from every caller
// view. Caller must hold g.mu.
func (g *Graph) removeLinkLocked(id string) {
	l, ok := g.links[id]
	if !ok {
		return
	}
	delete(g.incidentLinks[l.sourceID], id)
	delete(g.incidentLinks[l.targetID], id)
	delete(g.links, id)
	for _, v := range g.callers {
		g.deactivateLinkLocked(v, id)
		delete(v.traversalGranted, id)
	}
}

// sortedArcIDs returns a deterministic sorted copy of an arc set.
func sortedArcIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
