package ontology

import (
	"fmt"
	"slices"
	"strings"
)

// cascadePlan is the result of the planning phase of a cascade deletion.
// Nothing is applied while planning, so an abort leaves the store
// untouched; the apply phase executes the plan verbatim.
type cascadePlan struct {
	deleteObjs  []ObjectID
	delSet      map[ObjectID]bool
	removeLinks []linkKey
	removeSet   map[linkKey]bool
	skipped     []SkippedLink
	skipSet     map[linkKey]bool
	runCounts   map[ObjectID]map[LinkTypeID]int
}

func newCascadePlan() *cascadePlan {
	return &cascadePlan{
		delSet:    map[ObjectID]bool{},
		removeSet: map[linkKey]bool{},
		skipSet:   map[linkKey]bool{},
		runCounts: map[ObjectID]map[LinkTypeID]int{},
	}
}

// count returns the planned link count, lazily seeded from the engine.
func (p *cascadePlan) count(e *Engine, o ObjectID, lt LinkTypeID) int {
	m, ok := p.runCounts[o]
	if !ok {
		m = map[LinkTypeID]int{}
		p.runCounts[o] = m
	}
	if c, ok := m[lt]; ok {
		return c
	}
	c := e.count(o, lt)
	m[lt] = c
	return c
}

func (p *cascadePlan) decr(e *Engine, o ObjectID, lt LinkTypeID) {
	p.count(e, o, lt)
	p.runCounts[o][lt]--
}

// endPropsOf returns the participation properties under which object o
// sits in the given link (one entry per end it occupies; two for a
// self-loop).
func endPropsOf(lt *LinkType, k linkKey, o ObjectID) []PropertyID {
	var props []PropertyID
	if k.src == o {
		props = append(props, lt.Source.Property)
	}
	if k.dst == o {
		props = append(props, lt.Target.Property)
	}
	return props
}

// otherEnd returns the endpoint of k opposite to o.
func otherEnd(k linkKey, o ObjectID) ObjectID {
	if k.src == o {
		return k.dst
	}
	return k.src
}

// endOf resolves which declared end object o occupies in link k. The
// caller guarantees o is exactly one of the two endpoints.
func endOf(lt *LinkType, k linkKey, o ObjectID) LinkEnd {
	if k.src == o && k.dst != o {
		return lt.Source
	}
	if k.dst == o && k.src != o {
		return lt.Target
	}
	return lt.Source
}

func sortedLinkKeys(set map[linkKey]struct{}) []linkKey {
	keys := make([]linkKey, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b linkKey) int {
		if a.lt != b.lt {
			return strings.Compare(string(a.lt), string(b.lt))
		}
		if a.src != b.src {
			return strings.Compare(string(a.src), string(b.src))
		}
		return strings.Compare(string(a.dst), string(b.dst))
	})
	return keys
}

// planCascade simulates the whole cascade deletion of root without
// mutating the store. It returns abort=true when an invisible
// participation property is encountered while the engine-wide
// InvisibleAbort policy is in effect.
//
// The cost is proportional to the number of links directly incident to
// the instances being deleted; iteration is sorted so the plan is
// deterministic.
func (e *Engine) planCascade(subject SubjectID, root ObjectID, res *OpResult, ent *LogEntry) (plan *cascadePlan, abort bool) {
	plan = newCascadePlan()
	queue := []ObjectID{root}
	plan.delSet[root] = true

	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		plan.deleteObjs = append(plan.deleteObjs, x)

		ltIDs := make([]LinkTypeID, 0, len(e.adj[x]))
		for ltID := range e.adj[x] {
			ltIDs = append(ltIDs, ltID)
		}
		slices.Sort(ltIDs)

		for _, ltID := range ltIDs {
			lt := e.linkTypes[ltID]
			for _, key := range sortedLinkKeys(e.adj[x][ltID]) {
				if plan.removeSet[key] || plan.skipSet[key] {
					continue
				}
				xProps := endPropsOf(lt, key, x)
				y := otherEnd(key, x)
				yProps := endPropsOf(lt, key, y)

				if e.cfg.Cascade == CascadeCheck {
					// Visibility: the subject must read the participation
					// property of the instance being cleaned.
					invisible := false
					for _, p := range xProps {
						if !e.checkPerm(res, ent, subject, x, p, "read") {
							invisible = true
						}
					}
					if invisible {
						if e.cfg.Invisible == InvisibleAbort {
							return nil, true
						}
						plan.skipped = append(plan.skipped, SkippedLink{Link: key.ref(), Reason: SkipInvisible})
						plan.skipSet[key] = true
						continue
					}
					// Write permission on both ends' participation
					// properties, exactly as for a direct DeleteLink.
					allowed := true
					for _, p := range xProps {
						if !e.checkPerm(res, ent, subject, x, p, "write") {
							allowed = false
						}
					}
					for _, p := range yProps {
						if !e.checkPerm(res, ent, subject, y, p, "write") {
							allowed = false
						}
					}
					if !allowed {
						plan.skipped = append(plan.skipped, SkippedLink{Link: key.ref(), Reason: SkipPermission})
						plan.skipSet[key] = true
						continue
					}
				}

				// Cardinality: the surviving other end must not drop
				// below its declared minimum. This holds in both cascade
				// modes so an unfinished cleanup never leaves an
				// observable constraint violation.
				_, yAlive := e.objects[y]
				if y != x && yAlive && !plan.delSet[y] {
					end := endOf(lt, key, y)
					before := plan.count(e, y, ltID)
					after := before - 1
					if !e.checkCard(res, ent, y, ltID, before, after, end.Card, end.Card.allowsAfterDelete(after)) {
						plan.skipped = append(plan.skipped, SkippedLink{Link: key.ref(), Reason: SkipCardinality})
						plan.skipSet[key] = true
						continue
					}
				}

				plan.removeLinks = append(plan.removeLinks, key)
				plan.removeSet[key] = true
				plan.decr(e, x, ltID)
				plan.decr(e, y, ltID)

				if y != x && yAlive && !plan.delSet[y] && endOf(lt, key, y).CascadeDelete {
					plan.delSet[y] = true
					queue = append(queue, y)
				}
			}
		}
	}
	return plan, false
}

// DeleteObject deletes one object instance and cleans up the links it
// participates in, possibly cascading further deletions along link types
// whose far end declares CascadeDelete.
//
// The behavior on the cleanup path is governed by the engine-wide
// Config: CascadeBypass runs with the system identity (no permission or
// visibility checks), CascadeCheck checks the initiating subject's
// permissions link by link and skips (reporting distinctly) any link it
// may not remove. In both modes a removal that would push a surviving
// instance below its minimum cardinality is skipped instead, so no
// constraint violation is ever observable.
func (e *Engine) DeleteObject(subject SubjectID, id ObjectID) *OpResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	res := &OpResult{Op: OpDeleteObject}
	ent := LogEntry{Op: OpDeleteObject, Subject: subject, Target: id}

	if _, ok := e.objects[id]; !ok {
		return e.reject(res, &ent, ClassObjectNotFound, fmt.Sprintf("object %q does not exist", id))
	}

	plan, abort := e.planCascade(subject, id, res, &ent)
	if abort {
		return e.reject(res, &ent, ClassCascadeAborted,
			fmt.Sprintf("cascade delete of %q aborted: invisible participation property under abort policy", id))
	}

	for _, key := range plan.removeLinks {
		delete(e.links, key)
		e.removeAdj(key.src, key)
		e.removeAdj(key.dst, key)
		e.bumpCount(key.src, key.lt, -1)
		e.bumpCount(key.dst, key.lt, -1)
		res.Cleaned = append(res.Cleaned, key.ref())
	}
	for _, obj := range plan.deleteObjs {
		delete(e.objects, obj)
		delete(e.adj, obj)
		delete(e.counts, obj)
	}
	res.Skipped = plan.skipped
	res.Deleted = plan.deleteObjs
	ent.Cleaned = res.Cleaned
	ent.Skipped = res.Skipped
	ent.Deleted = res.Deleted
	e.clock++
	return e.commit(res, &ent)
}
