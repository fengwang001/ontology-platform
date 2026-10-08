package ontology

import (
	"fmt"
	"sync"
)

type grantKey struct {
	subject  SubjectID
	object   ObjectID
	property PropertyID
}

type linkKey struct {
	lt  LinkTypeID
	src ObjectID
	dst ObjectID
}

func (k linkKey) ref() LinkRef { return LinkRef{Type: k.lt, Src: k.src, Dst: k.dst} }

// Engine is the link cardinality + property-level permission module.
//
// Concurrency: every public mutating operation runs under a single
// mutex, so any interleaving of concurrent calls is equivalent to the
// serial order in which the calls acquired the mutex. That serial order
// is exactly the order of the emitted log entries, which makes it
// observable and replayable.
type Engine struct {
	mu        sync.Mutex
	cfg       Config
	linkTypes map[LinkTypeID]*LinkType
	objTypes  map[ObjectTypeID]struct{}
	objects   map[ObjectID]ObjectTypeID
	links     map[linkKey]struct{}
	// adj indexes incident links per object per link type. Its size is
	// proportional to the object's direct associations only.
	adj    map[ObjectID]map[LinkTypeID]map[linkKey]struct{}
	counts map[ObjectID]map[LinkTypeID]int
	grants map[grantKey]Permission
	// clock advances only when a mutation is committed; rejected
	// operations never touch it.
	clock  uint64
	logSeq uint64
	logger Logger
	cum    OpStats
}

// NewEngine creates an engine. A nil logger discards log entries.
func NewEngine(cfg Config, logger Logger) *Engine {
	if logger == nil {
		logger = discardLogger{}
	}
	return &Engine{
		cfg:       cfg,
		linkTypes: map[LinkTypeID]*LinkType{},
		objTypes:  map[ObjectTypeID]struct{}{},
		objects:   map[ObjectID]ObjectTypeID{},
		links:     map[linkKey]struct{}{},
		adj:       map[ObjectID]map[LinkTypeID]map[linkKey]struct{}{},
		counts:    map[ObjectID]map[LinkTypeID]int{},
		grants:    map[grantKey]Permission{},
		logger:    logger,
	}
}

type discardLogger struct{}

func (discardLogger) Log(LogEntry) {}

// RegisterObjectType declares an object type.
func (e *Engine) RegisterObjectType(id ObjectTypeID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.objTypes[id]; ok {
		return fmt.Errorf("object type %q already registered", id)
	}
	e.objTypes[id] = struct{}{}
	return nil
}

// RegisterLinkType declares a link type. Both ends' object types must be
// registered first, and the cardinality interval must be well formed.
func (e *Engine) RegisterLinkType(lt LinkType) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.linkTypes[lt.ID]; ok {
		return fmt.Errorf("link type %q already registered", lt.ID)
	}
	for _, end := range []LinkEnd{lt.Source, lt.Target} {
		if _, ok := e.objTypes[end.ObjectType]; !ok {
			return fmt.Errorf("link type %q: unknown object type %q", lt.ID, end.ObjectType)
		}
		if end.Card.Min < 0 {
			return fmt.Errorf("link type %q: negative min cardinality", lt.ID)
		}
		if end.Card.Max >= 0 && end.Card.Max < end.Card.Min {
			return fmt.Errorf("link type %q: max < min cardinality", lt.ID)
		}
	}
	c := lt
	e.linkTypes[lt.ID] = &c
	return nil
}

// AddObject creates an object instance.
func (e *Engine) AddObject(id ObjectID, typ ObjectTypeID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.objTypes[typ]; !ok {
		return fmt.Errorf("unknown object type %q", typ)
	}
	if _, ok := e.objects[id]; ok {
		return fmt.Errorf("object %q already exists", id)
	}
	e.objects[id] = typ
	return nil
}

// SetGrant declares the read/write permission of one subject on one
// property of one object instance. Read and write are independent.
func (e *Engine) SetGrant(subject SubjectID, object ObjectID, property PropertyID, perm Permission) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.grants[grantKey{subject, object, property}] = perm
}

// Clock returns the current logical clock value.
func (e *Engine) Clock() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// CumulativeStats returns the sum of OpStats over all operations so far.
func (e *Engine) CumulativeStats() OpStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cum
}

// perm is an O(1) grant lookup; missing grants default to no access.
func (e *Engine) perm(s SubjectID, o ObjectID, p PropertyID) Permission {
	return e.grants[grantKey{s, o, p}]
}

func (e *Engine) count(o ObjectID, lt LinkTypeID) int {
	return e.counts[o][lt]
}

func (e *Engine) bumpCount(o ObjectID, lt LinkTypeID, delta int) {
	m, ok := e.counts[o]
	if !ok {
		m = map[LinkTypeID]int{}
		e.counts[o] = m
	}
	m[lt] += delta
	if m[lt] == 0 {
		delete(m, lt)
	}
}

func (e *Engine) addAdj(o ObjectID, k linkKey) {
	byType, ok := e.adj[o]
	if !ok {
		byType = map[LinkTypeID]map[linkKey]struct{}{}
		e.adj[o] = byType
	}
	set, ok := byType[k.lt]
	if !ok {
		set = map[linkKey]struct{}{}
		byType[k.lt] = set
	}
	set[k] = struct{}{}
}

func (e *Engine) removeAdj(o ObjectID, k linkKey) {
	byType, ok := e.adj[o]
	if !ok {
		return
	}
	set, ok := byType[k.lt]
	if !ok {
		return
	}
	delete(set, k)
	if len(set) == 0 {
		delete(byType, k.lt)
	}
	if len(byType) == 0 {
		delete(e.adj, o)
	}
}

// reject finalizes a rejected operation: no link, count or clock state is
// touched; the decision and its basis are logged.
func (e *Engine) reject(res *OpResult, ent *LogEntry, class ErrorClass, msg string) *OpResult {
	res.Err = &OpError{Class: class, Msg: msg}
	res.Clock = e.clock
	ent.ErrClass = class
	ent.ClockAfter = e.clock
	e.emit(res, ent)
	return res
}

// commit finalizes an accepted operation.
func (e *Engine) commit(res *OpResult, ent *LogEntry) *OpResult {
	res.OK = true
	res.Clock = e.clock
	ent.ClockAfter = e.clock
	e.emit(res, ent)
	return res
}

func (e *Engine) emit(res *OpResult, ent *LogEntry) {
	ent.Seq = e.logSeq
	e.logSeq++
	e.cum.add(res.Stats)
	e.logger.Log(*ent)
}

// checkPerm performs one permission check, recording it in the per-op
// stats and in the log basis.
func (e *Engine) checkPerm(res *OpResult, ent *LogEntry, s SubjectID, o ObjectID, p PropertyID, need string) bool {
	res.Stats.PermissionChecks++
	perm := e.perm(s, o, p)
	granted := perm.Write
	if need == "read" {
		granted = perm.Read
	}
	ent.PermChecks = append(ent.PermChecks, PermCheckRecord{
		Subject: s, Object: o, Property: p, Need: need, Granted: granted,
	})
	return granted
}

// checkCard performs one cardinality evaluation, recording it in the
// per-op stats and in the log basis.
func (e *Engine) checkCard(res *OpResult, ent *LogEntry, o ObjectID, lt LinkTypeID, before, after int, c Cardinality, ok bool) bool {
	res.Stats.CardinalityChecks++
	ent.CardChecks = append(ent.CardChecks, CardCheckRecord{
		Object: o, LinkType: lt, Before: before, After: after,
		Min: c.Min, Max: c.Max, OK: ok,
	})
	return ok
}

// normalize orders the endpoints of undirected link types so that (a,b)
// and (b,a) denote the same link.
func normalize(lt *LinkType, src, dst ObjectID) (ObjectID, ObjectID) {
	if !lt.Directed && dst < src {
		return dst, src
	}
	return src, dst
}

// CreateLink creates one link. It is allowed only if, after the creation,
// both endpoints remain within their declared cardinality interval and
// the subject holds write permission on the participation property of
// both endpoints. A rejection leaves no trace on either end.
func (e *Engine) CreateLink(subject SubjectID, ltID LinkTypeID, src, dst ObjectID) *OpResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	res := &OpResult{Op: OpCreateLink}
	ent := LogEntry{Op: OpCreateLink, Subject: subject, LinkType: ltID, Src: src, Dst: dst}

	lt, ok := e.linkTypes[ltID]
	if !ok {
		return e.reject(res, &ent, ClassUnknownLinkType, fmt.Sprintf("link type %q is not registered", ltID))
	}
	src, dst = normalize(lt, src, dst)
	ent.Src, ent.Dst = src, dst

	srcType, ok := e.objects[src]
	if !ok {
		return e.reject(res, &ent, ClassObjectNotFound, fmt.Sprintf("source object %q does not exist", src))
	}
	dstType, ok := e.objects[dst]
	if !ok {
		return e.reject(res, &ent, ClassObjectNotFound, fmt.Sprintf("target object %q does not exist", dst))
	}
	if srcType != lt.Source.ObjectType || dstType != lt.Target.ObjectType {
		return e.reject(res, &ent, ClassTypeMismatch,
			fmt.Sprintf("link type %q connects %q -> %q, got %q (%q) -> %q (%q)",
				ltID, lt.Source.ObjectType, lt.Target.ObjectType, src, srcType, dst, dstType))
	}
	key := linkKey{lt: ltID, src: src, dst: dst}
	if _, ok := e.links[key]; ok {
		return e.reject(res, &ent, ClassLinkExists, fmt.Sprintf("link %v already exists", key.ref()))
	}

	// Cardinality first, permissions second: a fixed priority order.
	// A self-loop occupies two slots on the same instance.
	delta := 1
	if src == dst {
		delta = 2
	}
	for _, end := range []struct {
		obj  ObjectID
		card Cardinality
	}{{src, lt.Source.Card}, {dst, lt.Target.Card}} {
		before := e.count(end.obj, ltID)
		after := before + delta
		if !e.checkCard(res, &ent, end.obj, ltID, before, after, end.card, end.card.allowsAfterCreate(after)) {
			return e.reject(res, &ent, ClassCardinalityViolation,
				fmt.Sprintf("object %q would hold %d links of type %q, outside [%d,%d]",
					end.obj, after, ltID, end.card.Min, end.card.Max))
		}
	}
	if !e.checkPerm(res, &ent, subject, src, lt.Source.Property, "write") {
		return e.reject(res, &ent, ClassPermissionDenied,
			fmt.Sprintf("subject %q lacks write on %q.%q", subject, src, lt.Source.Property))
	}
	if !e.checkPerm(res, &ent, subject, dst, lt.Target.Property, "write") {
		return e.reject(res, &ent, ClassPermissionDenied,
			fmt.Sprintf("subject %q lacks write on %q.%q", subject, dst, lt.Target.Property))
	}

	e.links[key] = struct{}{}
	e.addAdj(src, key)
	e.addAdj(dst, key)
	e.bumpCount(src, ltID, 1)
	e.bumpCount(dst, ltID, 1)
	e.clock++
	return e.commit(res, &ent)
}

// DeleteLink removes one link. Like creation, it requires write
// permission on both endpoints' participation properties, and the
// post-deletion counts must satisfy the declared lower bounds.
func (e *Engine) DeleteLink(subject SubjectID, ltID LinkTypeID, src, dst ObjectID) *OpResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	res := &OpResult{Op: OpDeleteLink}
	ent := LogEntry{Op: OpDeleteLink, Subject: subject, LinkType: ltID, Src: src, Dst: dst}

	lt, ok := e.linkTypes[ltID]
	if !ok {
		return e.reject(res, &ent, ClassUnknownLinkType, fmt.Sprintf("link type %q is not registered", ltID))
	}
	src, dst = normalize(lt, src, dst)
	ent.Src, ent.Dst = src, dst

	if _, ok := e.objects[src]; !ok {
		return e.reject(res, &ent, ClassObjectNotFound, fmt.Sprintf("source object %q does not exist", src))
	}
	if _, ok := e.objects[dst]; !ok {
		return e.reject(res, &ent, ClassObjectNotFound, fmt.Sprintf("target object %q does not exist", dst))
	}
	key := linkKey{lt: ltID, src: src, dst: dst}
	if _, ok := e.links[key]; !ok {
		return e.reject(res, &ent, ClassLinkNotFound, fmt.Sprintf("link %v does not exist", key.ref()))
	}

	delta := 1
	if src == dst {
		delta = 2
	}
	for _, end := range []struct {
		obj  ObjectID
		card Cardinality
	}{{src, lt.Source.Card}, {dst, lt.Target.Card}} {
		before := e.count(end.obj, ltID)
		after := before - delta
		if !e.checkCard(res, &ent, end.obj, ltID, before, after, end.card, end.card.allowsAfterDelete(after)) {
			return e.reject(res, &ent, ClassCardinalityViolation,
				fmt.Sprintf("object %q would hold %d links of type %q, outside [%d,%d]",
					end.obj, after, ltID, end.card.Min, end.card.Max))
		}
	}
	if !e.checkPerm(res, &ent, subject, src, lt.Source.Property, "write") {
		return e.reject(res, &ent, ClassPermissionDenied,
			fmt.Sprintf("subject %q lacks write on %q.%q", subject, src, lt.Source.Property))
	}
	if !e.checkPerm(res, &ent, subject, dst, lt.Target.Property, "write") {
		return e.reject(res, &ent, ClassPermissionDenied,
			fmt.Sprintf("subject %q lacks write on %q.%q", subject, dst, lt.Target.Property))
	}

	delete(e.links, key)
	e.removeAdj(src, key)
	e.removeAdj(dst, key)
	e.bumpCount(src, ltID, -1)
	e.bumpCount(dst, ltID, -1)
	e.clock++
	return e.commit(res, &ent)
}
