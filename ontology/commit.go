package ontology

import (
	"fmt"
	"sort"
)

func (s *Store) RegisterType(t TypeName, validators ...Validator) {
	s.typeMu.Lock()
	s.types[t] = append(append([]Validator(nil), s.types[t]...), validators...)
	s.typeMu.Unlock()
}

// Commit attempts one write. See DESIGN.md for the full protocol; in short:
// hook scopes are resolved on the declared baseline, all involved instance
// locks are taken in sorted order, and adjudication is recomputed against
// the then-latest committed state. Rejected writes mutate nothing.
func (s *Store) Commit(req WriteRequest) Result {
	if req.Object == "" {
		return Result{Kind: ConflictStaleBase, Err: fmt.Errorf("ontology: empty object id")}
	}

	// Phase 0: baseline snapshot and hook scope, without instance locks.
	target := s.stateFor(req.Object)
	target.mu.Lock()
	baseSnap := target.snapshotAt(req.Base)
	target.mu.Unlock()

	values := cloneValues(req.Values)
	if req.Delete {
		values = map[Property]Value{}
	}
	rs, _ := s.resolveScopes(req.Type, values, baseSnap)
	s.observeVersions(&rs, req.ObserveExternal)

	// Phase 1: lock every involved instance in globally sorted order.
	lockIDs := sortedLockSet(req.Object, rs.linkedIDs)
	locked := make(map[ObjectID]*objState, len(lockIDs))
	s.objMu.Lock()
	for _, id := range lockIDs {
		st := s.stateForLocked(id)
		st.mu.Lock()
		locked[id] = st
	}
	s.objMu.Unlock()
	defer func() {
		for _, st := range locked {
			st.mu.Unlock()
		}
	}()

	cur := locked[req.Object]
	d := newDecision(req, rs)

	// Rejected writes are recorded but never decided or applied.
	if verr := s.runValidators(req.Type, values, cur, rs, locked, &d); verr != nil {
		d.RejectedByValidator = true
		d.ValidatorErr = verr.Error()
		cur.decisions = append(cur.decisions, d)
		return Result{Err: fmt.Errorf("%w: %v", ErrRejected, verr)}
	}

	// Adjudication order: deleted > duplicate > stale baseline > property.
	// The three conflict classes are mutually exclusive.
	// Deleted check precedes every other conflict class.
	if cur.exists && cur.versions[cur.head].deleted {
		return s.reject(cur, &d, ConflictDeleted, "instance is logically deleted")
	}

	if req.IdempotencyKey != "" {
		if rec, ok := s.lookupIdem(req.Object, req.IdempotencyKey); ok {
			d.Reason = "duplicate idempotency key"
			return s.rejectLocked(cur, &d, ConflictStaleBase, "duplicate write", rec.version)
		}
	}

	if req.Create {
		if cur.exists && !cur.versions[cur.head].deleted {
			return s.reject(cur, &d, ConflictStaleBase, "create on existing instance")
		}
		if req.Base != 0 {
			return s.reject(cur, &d, ConflictStaleBase, "create baseline must be 0")
		}
	} else {
		if !cur.exists {
			return s.reject(cur, &d, ConflictStaleBase, "unknown instance")
		}
		if baseSnap == nil || req.Base > cur.head {
			return s.reject(cur, &d, ConflictStaleBase,
				fmt.Sprintf("baseline v%d not usable (head v%d)", req.Base, cur.head))
		}
	}

	d.HeadAtCommit = cur.head
	computeRelevant(&d, req.Delete, rs)
	if ev := s.checkConflict(cur, locked, &d, rs); ev != nil {
		return s.rejectLocked(cur, &d, ConflictProperty,
			fmt.Sprintf("%s.%s changed since baseline (observed v%d, committed v%d)",
				ev.Object, ev.Property, ev.Observed, ev.Committed), 0)
	}

	// Apply: strictly monotonic per-instance version, one clock tick.
	newVer := cur.head + 1
	var newProps []snapshotProp
	if req.Create || len(cur.versions) == 0 {
		newProps = mergeProps(nil, values)
	} else {
		newProps = mergeProps(cur.versions[cur.head].props, values)
	}
	snap := encodeSnapshot(newVer, req.Delete, newProps)
	if int(newVer) == len(cur.versions) {
		cur.versions = append(cur.versions, snap)
	} else {
		cur.versions[newVer] = snap
	}
	if len(cur.versions) == 1 {
		cur.versions = append([]*snapshot{nil}, cur.versions...)
	}
	cur.exists = true
	cur.head = newVer

	s.updateMarkers(cur, locked, newVer, &d)

	clock := s.clock.Add(1)

	d.Sequence = clock
	d.Accepted = true
	d.NewVersion = newVer
	cur.decisions = append(cur.decisions, d)

	if req.IdempotencyKey != "" {
		s.putIdem(req.Object, req.IdempotencyKey, idemRecord{version: newVer})
	}
	return Result{NewVersion: newVer}
}

func (s *Store) Read(id ObjectID, v Version) (Snapshot, bool) {
	st := s.stateFor(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.exists || v <= 0 || int(v) >= len(st.versions) {
		return nil, false
	}
	return st.versions[v], true
}

func (s *Store) Head(id ObjectID) (Version, bool) {
	st := s.stateFor(id)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.exists {
		return 0, false
	}
	return st.head, true
}

func sortedLockSet(target ObjectID, linked []ObjectID) []ObjectID {
	set := map[ObjectID]bool{target: true}
	for _, id := range linked {
		if id != "" {
			set[id] = true
		}
	}
	out := make([]ObjectID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func newDecision(req WriteRequest, rs resolvedScope) Decision {
	d := Decision{
		Object:         req.Object,
		Type:           req.Type,
		IdempotencyKey: req.IdempotencyKey,
		DeclaredBase:   req.Base,
		Observed:       map[ObjectID]Version{},
	}
	for k := range req.Values {
		d.WriteSet = append(d.WriteSet, k)
	}
	sort.Slice(d.WriteSet, func(i, j int) bool { return d.WriteSet[i] < d.WriteSet[j] })
	for _, r := range rs.refs {
		d.ReadScope = append(d.ReadScope, PropertyRef{Instance: r.target, Link: r.link, Local: r.prop})
	}
	for id, v := range rs.observed {
		d.Observed[id] = v
	}
	return d
}

func computeRelevant(d *Decision, deleteWrite bool, rs resolvedScope) {
	local := map[Property]bool{}
	ext := map[ObjectID]map[Property]bool{}
	addLocal := func(p Property) { local[p] = true }
	addExt := func(id ObjectID, p Property) {
		if id == "" {
			local[p] = true
			return
		}
		m := ext[id]
		if m == nil {
			m = map[Property]bool{}
			ext[id] = m
		}
		m[p] = true
	}
	for _, p := range d.WriteSet {
		addLocal(p)
	}
	for _, r := range rs.refs {
		if r.link == "" {
			addLocal(r.prop)
		} else {
			addExt(r.target, r.prop)
		}
	}
	for p := range local {
		d.RelevantLocal = append(d.RelevantLocal, p)
	}
	sort.Slice(d.RelevantLocal, func(i, j int) bool { return d.RelevantLocal[i] < d.RelevantLocal[j] })
	if deleteWrite {
		d.RelevantLocal = append(d.RelevantLocal, deleteSentinel)
		sort.Slice(d.RelevantLocal, func(i, j int) bool { return d.RelevantLocal[i] < d.RelevantLocal[j] })
	}
	ids := make([]ObjectID, 0, len(ext))
	for id := range ext {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	d.RelevantExternal = map[ObjectID][]Property{}
	for _, id := range ids {
		ps := make([]Property, 0, len(ext[id]))
		for p := range ext[id] {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		d.RelevantExternal[id] = ps
	}
}

// checkConflict performs O(|relevant set|) marker comparisons. Markers are
// per-property latest-touch versions, so the cost never depends on the
// number of historical versions.
func (s *Store) checkConflict(cur *objState, locked map[ObjectID]*objState, d *Decision, rs resolvedScope) *ConflictEvidence {
	base := d.DeclaredBase
	for _, p := range d.RelevantLocal {
		mv := cur.localMark[p]
		hit := mv > base
		shown := p
		if p == deleteSentinel {
			shown = Property("<delete>")
		}
		ev := ConflictEvidence{Object: d.Object, Property: shown, Via: "local", Observed: base, Committed: mv, Hit: hit}
		d.Evidence = append(d.Evidence, ev)
		if hit {
			return &ev
		}
	}
	for id, props := range d.RelevantExternal {
		other := locked[id]
		observed := d.Observed[id]
		for _, p := range props {
			var mv Version
			if other != nil {
				mv = other.localMark[p]
			}
			hit := other != nil && other.exists && mv > observed
			ev := ConflictEvidence{Object: id, Property: p, Via: "linked", Observed: observed, Committed: mv, Hit: hit}
			d.Evidence = append(d.Evidence, ev)
			if hit {
				return &ev
			}
		}
	}
	return nil
}

func (s *Store) updateMarkers(cur *objState, locked map[ObjectID]*objState, v Version, d *Decision) {
	for _, p := range d.RelevantLocal {
		key := p
		if p == "" {
			key = deleteSentinel
		}
		cur.localMark[key] = v
	}
	// Local write of any property bumps the marker observed by readers of
	// linked instances: external readers check the *other* instance's
	// markers directly, so nothing is needed here for external props.
	_ = locked
}

const deleteSentinel Property = "\x00deleted"

func (s *Store) reject(cur *objState, d *Decision, kind ConflictKind, reason string) Result {
	return s.rejectLocked(cur, d, kind, reason, 0)
}

func (s *Store) rejectLocked(cur *objState, d *Decision, kind ConflictKind, reason string, priorVersion Version) Result {
	d.Kind = kind
	d.Reason = reason
	d.Sequence = s.clock.Load()
	cur.decisions = append(cur.decisions, *d)
	var err error
	switch kind {
	case ConflictDeleted:
		err = fmt.Errorf("ontology: %w", ErrInstanceDeleted)
	case ConflictStaleBase:
		err = fmt.Errorf("ontology: %w: %s", ErrStaleBase, reason)
	case ConflictProperty:
		err = fmt.Errorf("ontology: %w: %s", ErrPropertyConflict, reason)
	}
	res := Result{Kind: kind, Err: err}
	if priorVersion != 0 {
		res.NewVersion = priorVersion
	}
	return res
}
