// Package reference is a deliberately naive, independently maintained
// implementation of the same link cardinality + permission semantics as
// package ontology. It keeps link types, objects, links and grants in
// flat slices and recomputes everything by linear scans, so it carries
// no index structures that could share bugs with the indexed engine.
// The differential test compares the two implementations operation by
// operation on randomly generated graphs and operation sequences.
package reference

import (
	"fmt"
	"slices"

	"ontology"
)

type objectEntry struct {
	id  ontology.ObjectID
	typ ontology.ObjectTypeID
}

type grantEntry struct {
	subject  ontology.SubjectID
	object   ontology.ObjectID
	property ontology.PropertyID
	perm     ontology.Permission
}

// Engine is the naive reference implementation.
type Engine struct {
	cfg       ontology.Config
	objTypes  []ontology.ObjectTypeID
	linkTypes []ontology.LinkType
	objects   []objectEntry
	links     []ontology.LinkRef
	grants    []grantEntry
	clock     uint64
	// Work counts naive scan steps. It grows with the total number of
	// links and grants, in contrast to the indexed engine whose
	// observable per-op check counts stay constant.
	Work uint64
}

// New creates an empty reference engine.
func New(cfg ontology.Config) *Engine {
	return &Engine{cfg: cfg}
}

// RegisterObjectType declares an object type.
func (e *Engine) RegisterObjectType(id ontology.ObjectTypeID) error {
	if slices.Contains(e.objTypes, id) {
		return fmt.Errorf("object type %q already registered", id)
	}
	e.objTypes = append(e.objTypes, id)
	return nil
}

// RegisterLinkType declares a link type.
func (e *Engine) RegisterLinkType(lt ontology.LinkType) error {
	for _, existing := range e.linkTypes {
		if existing.ID == lt.ID {
			return fmt.Errorf("link type %q already registered", lt.ID)
		}
	}
	for _, end := range []ontology.LinkEnd{lt.Source, lt.Target} {
		if !slices.Contains(e.objTypes, end.ObjectType) {
			return fmt.Errorf("link type %q: unknown object type %q", lt.ID, end.ObjectType)
		}
		if end.Card.Min < 0 {
			return fmt.Errorf("link type %q: negative min cardinality", lt.ID)
		}
		if end.Card.Max >= 0 && end.Card.Max < end.Card.Min {
			return fmt.Errorf("link type %q: max < min cardinality", lt.ID)
		}
	}
	e.linkTypes = append(e.linkTypes, lt)
	return nil
}

// AddObject creates an object instance.
func (e *Engine) AddObject(id ontology.ObjectID, typ ontology.ObjectTypeID) error {
	if !slices.Contains(e.objTypes, typ) {
		return fmt.Errorf("unknown object type %q", typ)
	}
	if _, ok := e.findObject(id); ok {
		return fmt.Errorf("object %q already exists", id)
	}
	e.objects = append(e.objects, objectEntry{id: id, typ: typ})
	return nil
}

// SetGrant declares the read/write permission of one subject on one
// property of one object instance. The last write wins.
func (e *Engine) SetGrant(subject ontology.SubjectID, object ontology.ObjectID, property ontology.PropertyID, perm ontology.Permission) {
	for i := range e.grants {
		g := &e.grants[i]
		if g.subject == subject && g.object == object && g.property == property {
			g.perm = perm
			return
		}
	}
	e.grants = append(e.grants, grantEntry{subject: subject, object: object, property: property, perm: perm})
}

// Clock returns the current logical clock value.
func (e *Engine) Clock() uint64 { return e.clock }

func (e *Engine) findLinkType(id ontology.LinkTypeID) *ontology.LinkType {
	for i := range e.linkTypes {
		e.Work++
		if e.linkTypes[i].ID == id {
			return &e.linkTypes[i]
		}
	}
	return nil
}

func (e *Engine) findObject(id ontology.ObjectID) (ontology.ObjectTypeID, bool) {
	for _, o := range e.objects {
		e.Work++
		if o.id == id {
			return o.typ, true
		}
	}
	return "", false
}

// countLinks recomputes the number of link endpoints object id holds for
// link type lt by scanning every link in the system.
func (e *Engine) countLinks(id ontology.ObjectID, lt ontology.LinkTypeID) int {
	n := 0
	for _, l := range e.links {
		e.Work++
		if l.Type != lt {
			continue
		}
		if l.Src == id {
			n++
		}
		if l.Dst == id {
			n++
		}
	}
	return n
}

func (e *Engine) permGranted(s ontology.SubjectID, o ontology.ObjectID, p ontology.PropertyID, need string) bool {
	for i := len(e.grants) - 1; i >= 0; i-- {
		e.Work++
		g := e.grants[i]
		if g.subject == s && g.object == o && g.property == p {
			if need == "read" {
				return g.perm.Read
			}
			return g.perm.Write
		}
	}
	return false
}

func (e *Engine) hasLink(lt ontology.LinkTypeID, src, dst ontology.ObjectID) bool {
	for _, l := range e.links {
		e.Work++
		if l.Type == lt && l.Src == src && l.Dst == dst {
			return true
		}
	}
	return false
}

func normalize(lt *ontology.LinkType, src, dst ontology.ObjectID) (ontology.ObjectID, ontology.ObjectID) {
	if !lt.Directed && dst < src {
		return dst, src
	}
	return src, dst
}

func allowsAfterCreate(c ontology.Cardinality, n int) bool { return c.Max < 0 || n <= c.Max }

func allowsAfterDelete(c ontology.Cardinality, n int) bool { return n >= c.Min }

func (e *Engine) checkPerm(res *ontology.OpResult, s ontology.SubjectID, o ontology.ObjectID, p ontology.PropertyID, need string) bool {
	res.Stats.PermissionChecks++
	return e.permGranted(s, o, p, need)
}

func (e *Engine) checkCard(res *ontology.OpResult, ok bool) bool {
	res.Stats.CardinalityChecks++
	return ok
}

func reject(res *ontology.OpResult, clock uint64, class ontology.ErrorClass, msg string) *ontology.OpResult {
	res.Err = &ontology.OpError{Class: class, Msg: msg}
	res.Clock = clock
	return res
}

// CreateLink mirrors ontology.Engine.CreateLink with naive scans.
func (e *Engine) CreateLink(subject ontology.SubjectID, ltID ontology.LinkTypeID, src, dst ontology.ObjectID) *ontology.OpResult {
	res := &ontology.OpResult{Op: ontology.OpCreateLink}

	lt := e.findLinkType(ltID)
	if lt == nil {
		return reject(res, e.clock, ontology.ClassUnknownLinkType, fmt.Sprintf("link type %q is not registered", ltID))
	}
	src, dst = normalize(lt, src, dst)

	srcType, ok := e.findObject(src)
	if !ok {
		return reject(res, e.clock, ontology.ClassObjectNotFound, fmt.Sprintf("source object %q does not exist", src))
	}
	dstType, ok := e.findObject(dst)
	if !ok {
		return reject(res, e.clock, ontology.ClassObjectNotFound, fmt.Sprintf("target object %q does not exist", dst))
	}
	if srcType != lt.Source.ObjectType || dstType != lt.Target.ObjectType {
		return reject(res, e.clock, ontology.ClassTypeMismatch, "endpoint types do not match link type")
	}
	if e.hasLink(ltID, src, dst) {
		return reject(res, e.clock, ontology.ClassLinkExists, "link already exists")
	}

	delta := 1
	if src == dst {
		delta = 2
	}
	for _, end := range []struct {
		obj  ontology.ObjectID
		card ontology.Cardinality
	}{{src, lt.Source.Card}, {dst, lt.Target.Card}} {
		before := e.countLinks(end.obj, ltID)
		after := before + delta
		ok := end.card.Max < 0 || after <= end.card.Max
		if !e.checkCard(res, ok) {
			return reject(res, e.clock, ontology.ClassCardinalityViolation, "max cardinality exceeded")
		}
	}
	res.Stats.PermissionChecks++
	if !e.permGranted(subject, src, lt.Source.Property, "write") {
		return reject(res, e.clock, ontology.ClassPermissionDenied, "missing write on source participation property")
	}
	res.Stats.PermissionChecks++
	if !e.permGranted(subject, dst, lt.Target.Property, "write") {
		return reject(res, e.clock, ontology.ClassPermissionDenied, "missing write on target participation property")
	}

	e.links = append(e.links, ontology.LinkRef{Type: ltID, Src: src, Dst: dst})
	e.clock++
	res.OK = true
	res.Clock = e.clock
	return res
}

// DeleteLink mirrors ontology.Engine.DeleteLink with naive scans.
func (e *Engine) DeleteLink(subject ontology.SubjectID, ltID ontology.LinkTypeID, src, dst ontology.ObjectID) *ontology.OpResult {
	res := &ontology.OpResult{Op: ontology.OpDeleteLink}

	lt := e.findLinkType(ltID)
	if lt == nil {
		return reject(res, e.clock, ontology.ClassUnknownLinkType, fmt.Sprintf("link type %q is not registered", ltID))
	}
	src, dst = normalize(lt, src, dst)

	if _, ok := e.findObject(src); !ok {
		return reject(res, e.clock, ontology.ClassObjectNotFound, fmt.Sprintf("source object %q does not exist", src))
	}
	if _, ok := e.findObject(dst); !ok {
		return reject(res, e.clock, ontology.ClassObjectNotFound, fmt.Sprintf("target object %q does not exist", dst))
	}
	idx := -1
	for i, l := range e.links {
		e.Work++
		if l.Type == ltID && l.Src == src && l.Dst == dst {
			idx = i
			break
		}
	}
	if idx < 0 {
		return reject(res, e.clock, ontology.ClassLinkNotFound, "link does not exist")
	}

	delta := 1
	if src == dst {
		delta = 2
	}
	for _, end := range []struct {
		obj  ontology.ObjectID
		card ontology.Cardinality
	}{{src, lt.Source.Card}, {dst, lt.Target.Card}} {
		before := e.countLinks(end.obj, ltID)
		after := before - delta
		ok := after >= end.card.Min
		if !e.checkCard(res, ok) {
			return reject(res, e.clock, ontology.ClassCardinalityViolation, "min cardinality violated")
		}
	}
	res.Stats.PermissionChecks++
	if !e.permGranted(subject, src, lt.Source.Property, "write") {
		return reject(res, e.clock, ontology.ClassPermissionDenied, "missing write on source participation property")
	}
	res.Stats.PermissionChecks++
	if !e.permGranted(subject, dst, lt.Target.Property, "write") {
		return reject(res, e.clock, ontology.ClassPermissionDenied, "missing write on target participation property")
	}

	e.links = slices.Delete(e.links, idx, idx+1)
	e.clock++
	res.OK = true
	res.Clock = e.clock
	return res
}

type refPlan struct {
	deleteObjs  []ontology.ObjectID
	delSet      map[ontology.ObjectID]bool
	removeLinks []ontology.LinkRef
	removeSet   map[ontology.LinkRef]bool
	skipped     []ontology.SkippedLink
	skipSet     map[ontology.LinkRef]bool
	runCounts   map[ontology.ObjectID]map[ontology.LinkTypeID]int
}

func (p *refPlan) count(e *Engine, o ontology.ObjectID, lt ontology.LinkTypeID) int {
	m, ok := p.runCounts[o]
	if !ok {
		m = map[ontology.LinkTypeID]int{}
		p.runCounts[o] = m
	}
	if c, ok := m[lt]; ok {
		return c
	}
	c := e.countLinks(o, lt)
	m[lt] = c
	return c
}

func (p *refPlan) decr(e *Engine, o ontology.ObjectID, lt ontology.LinkTypeID) {
	p.count(e, o, lt)
	p.runCounts[o][lt]--
}

func endPropsOf(lt *ontology.LinkType, l ontology.LinkRef, o ontology.ObjectID) []ontology.PropertyID {
	var props []ontology.PropertyID
	if l.Src == o {
		props = append(props, lt.Source.Property)
	}
	if l.Dst == o {
		props = append(props, lt.Target.Property)
	}
	return props
}

func otherEnd(l ontology.LinkRef, o ontology.ObjectID) ontology.ObjectID {
	if l.Src == o {
		return l.Dst
	}
	return l.Src
}

func endOf(lt *ontology.LinkType, l ontology.LinkRef, o ontology.ObjectID) ontology.LinkEnd {
	if l.Src == o && l.Dst != o {
		return lt.Source
	}
	return lt.Target
}

// incidentLinks collects the links incident to x by scanning every link,
// grouped per link type and deterministically sorted, matching the
// indexed engine's iteration order exactly.
func (e *Engine) incidentLinks(x ontology.ObjectID) map[ontology.LinkTypeID][]ontology.LinkRef {
	out := map[ontology.LinkTypeID][]ontology.LinkRef{}
	for _, l := range e.links {
		e.Work++
		if l.Src == x || l.Dst == x {
			out[l.Type] = append(out[l.Type], l)
		}
	}
	for lt := range out {
		slices.SortFunc(out[lt], func(a, b ontology.LinkRef) int {
			if a.Src != b.Src {
				if a.Src < b.Src {
					return -1
				}
				return 1
			}
			if a.Dst != b.Dst {
				if a.Dst < b.Dst {
					return -1
				}
				return 1
			}
			return 0
		})
	}
	return out
}

func (e *Engine) planCascade(subject ontology.SubjectID, root ontology.ObjectID, res *ontology.OpResult) (plan *refPlan, abort bool) {
	plan = &refPlan{
		delSet:    map[ontology.ObjectID]bool{},
		removeSet: map[ontology.LinkRef]bool{},
		skipSet:   map[ontology.LinkRef]bool{},
		runCounts: map[ontology.ObjectID]map[ontology.LinkTypeID]int{},
	}
	queue := []ontology.ObjectID{root}
	plan.delSet[root] = true

	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		plan.deleteObjs = append(plan.deleteObjs, x)

		incident := e.incidentLinks(x)
		ltIDs := make([]ontology.LinkTypeID, 0, len(incident))
		for ltID := range incident {
			ltIDs = append(ltIDs, ltID)
		}
		slices.Sort(ltIDs)

		for _, ltID := range ltIDs {
			lt := e.findLinkType(ltID)
			for _, l := range incident[ltID] {
				if plan.removeSet[l] || plan.skipSet[l] {
					continue
				}
				xProps := endPropsOf(lt, l, x)
				y := otherEnd(l, x)
				yProps := endPropsOf(lt, l, y)

				if e.cfg.Cascade == ontology.CascadeCheck {
					invisible := false
					for _, p := range xProps {
						res.Stats.PermissionChecks++
						if !e.permGranted(subject, x, p, "read") {
							invisible = true
						}
					}
					if invisible {
						if e.cfg.Invisible == ontology.InvisibleAbort {
							return nil, true
						}
						plan.skipped = append(plan.skipped, ontology.SkippedLink{Link: l, Reason: ontology.SkipInvisible})
						plan.skipSet[l] = true
						continue
					}
					allowed := true
					for _, p := range xProps {
						res.Stats.PermissionChecks++
						if !e.permGranted(subject, x, p, "write") {
							allowed = false
						}
					}
					for _, p := range yProps {
						res.Stats.PermissionChecks++
						if !e.permGranted(subject, y, p, "write") {
							allowed = false
						}
					}
					if !allowed {
						plan.skipped = append(plan.skipped, ontology.SkippedLink{Link: l, Reason: ontology.SkipPermission})
						plan.skipSet[l] = true
						continue
					}
				}

				_, yAlive := e.findObject(y)
				if y != x && yAlive && !plan.delSet[y] {
					end := endOf(lt, l, y)
					before := plan.count(e, y, ltID)
					after := before - 1
					ok := after >= end.Card.Min
					res.Stats.CardinalityChecks++
					if !ok {
						plan.skipped = append(plan.skipped, ontology.SkippedLink{Link: l, Reason: ontology.SkipCardinality})
						plan.skipSet[l] = true
						continue
					}
				}

				plan.removeLinks = append(plan.removeLinks, l)
				plan.removeSet[l] = true
				plan.decr(e, x, ltID)
				plan.decr(e, y, ltID)

				if y != x && yAlive && !plan.delSet[y] && endOf(lt, l, y).CascadeDelete {
					plan.delSet[y] = true
					queue = append(queue, y)
				}
			}
		}
	}
	return plan, false
}

// DeleteObject mirrors ontology.Engine.DeleteObject with naive scans.
func (e *Engine) DeleteObject(subject ontology.SubjectID, id ontology.ObjectID) *ontology.OpResult {
	res := &ontology.OpResult{Op: ontology.OpDeleteObject}

	if _, ok := e.findObject(id); !ok {
		return reject(res, e.clock, ontology.ClassObjectNotFound, fmt.Sprintf("object %q does not exist", id))
	}

	plan, abort := e.planCascade(subject, id, res)
	if abort {
		return reject(res, e.clock, ontology.ClassCascadeAborted, "invisible participation property under abort policy")
	}

	kept := e.links[:0]
	for _, l := range e.links {
		if !plan.removeSet[l] {
			kept = append(kept, l)
		}
	}
	e.links = kept
	res.Cleaned = append(res.Cleaned, plan.removeLinks...)

	keptObjs := e.objects[:0]
	for _, o := range e.objects {
		if !plan.delSet[o.id] {
			keptObjs = append(keptObjs, o)
		}
	}
	e.objects = keptObjs

	res.Skipped = plan.skipped
	res.Deleted = plan.deleteObjs
	e.clock++
	res.OK = true
	res.Clock = e.clock
	return res
}

// Snapshot recomputes a canonical view of the state by scanning.
func (e *Engine) Snapshot() ontology.Snapshot {
	snap := ontology.Snapshot{
		Objects: make(map[ontology.ObjectID]ontology.ObjectTypeID, len(e.objects)),
		Counts:  map[ontology.ObjectID]map[ontology.LinkTypeID]int{},
		Clock:   e.clock,
	}
	for _, o := range e.objects {
		snap.Objects[o.id] = o.typ
	}
	snap.Links = append(snap.Links, e.links...)
	slices.SortFunc(snap.Links, func(a, b ontology.LinkRef) int {
		if a.Type != b.Type {
			if a.Type < b.Type {
				return -1
			}
			return 1
		}
		if a.Src != b.Src {
			if a.Src < b.Src {
				return -1
			}
			return 1
		}
		if a.Dst != b.Dst {
			if a.Dst < b.Dst {
				return -1
			}
			return 1
		}
		return 0
	})
	for _, o := range e.objects {
		for _, lt := range e.linkTypes {
			if c := e.countLinks(o.id, lt.ID); c > 0 {
				m, ok := snap.Counts[o.id]
				if !ok {
					m = map[ontology.LinkTypeID]int{}
					snap.Counts[o.id] = m
				}
				m[lt.ID] = c
			}
		}
	}
	return snap
}
