package temporal

import (
	"fmt"
	"sort"
)

// Commit atomically assigns the next instant to all staged changes, applies
// cardinality validation against the post-commit state, cascades link
// revocation for deleted objects, publishes every timeline/root and appends
// one entry to the global commit log.
func (tx *Tx) Commit() (Instant, error) {
	s := tx.store
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	at := s.head.get() + 1

	if err := tx.precheck(at); err != nil {
		return 0, err
	}
	if err := tx.validate(at); err != nil {
		return 0, err
	}

	tx.cascadeDeletes(at)

	tx.applyTypes(at)
	tx.applyObjects(at)
	tx.applyEdges(at)

	s.head.set(at)
	s.appendLogLocked(tx.buildEntry(at))

	return at, nil
}

// precheck rejects staged writes that refer to things that do not exist at
// the pre-commit instant. It runs before any mutation, so a rejected commit
// leaves every historical timeline untouched.
func (tx *Tx) precheck(at Instant) error {
	s := tx.store
	prev := at - 1

	for id := range tx.createObjectTypes {
		if s.objectTypes.get(id) != nil {
			return &StagingError{msg: fmt.Sprintf("object type %q already exists", id)}
		}
	}
	for id, props := range tx.migrateTypes {
		if _, ok := tx.createObjectTypes[id]; ok {
			continue
		}
		tl := s.objectTypes.get(id)
		if tl == nil {
			return &StagingError{msg: fmt.Sprintf("cannot migrate unknown object type %q", id)}
		}
		if e, ok := tl.at(prev); !ok || !e.alive {
			return &StagingError{msg: fmt.Sprintf("cannot migrate object type %q: no live version", id)}
		}
		_ = props
	}
	for id := range tx.createLinkTypes {
		if s.linkTypes.get(id) != nil {
			return &StagingError{msg: fmt.Sprintf("link type %q already exists", id)}
		}
	}
	for id := range tx.adjustCards {
		if _, ok := tx.createLinkTypes[id]; ok {
			continue
		}
		tl := s.linkTypes.get(id)
		if tl == nil {
			return &StagingError{msg: fmt.Sprintf("cannot adjust cardinality of unknown link type %q", id)}
		}
		if _, ok := tl.at(prev); !ok {
			return &StagingError{msg: fmt.Sprintf("cannot adjust cardinality of link type %q: no live version", id)}
		}
	}

	for id, rec := range tx.createObjects {
		if _, creatingType := tx.createObjectTypes[rec.typeID]; !creatingType {
			tl := s.objectTypes.get(rec.typeID)
			if tl == nil {
				return &StagingError{msg: fmt.Sprintf("object %q references unknown type %q", id, rec.typeID)}
			}
			if e, ok := tl.at(prev); !ok || !e.alive {
				return &StagingError{msg: fmt.Sprintf("object %q references non-live type %q", id, rec.typeID)}
			}
		}
		if tl := s.objects.get(id); tl != nil {
			if e, ok := tl.at(prev); ok && e.alive {
				return &StagingError{msg: fmt.Sprintf("object %q already exists", id)}
			}
		}
	}
	for id := range tx.setProps {
		if _, creating := tx.createObjects[id]; creating {
			continue
		}
		tl := s.objects.get(id)
		if tl == nil {
			return &StagingError{msg: fmt.Sprintf("SetProperties on missing object %q", id)}
		}
		if e, ok := tl.at(prev); !ok || !e.alive {
			return &StagingError{msg: fmt.Sprintf("SetProperties on deleted object %q", id)}
		}
	}
	for id := range tx.deleteObjects {
		if _, recreating := tx.createObjects[id]; recreating {
			return &StagingError{msg: fmt.Sprintf("object %q both deleted and recreated in one commit", id)}
		}
		tl := s.objects.get(id)
		if tl == nil {
			return &StagingError{msg: fmt.Sprintf("cannot delete missing object %q", id)}
		}
		if e, ok := tl.at(prev); !ok || !e.alive {
			return &StagingError{msg: fmt.Sprintf("cannot delete non-live object %q", id)}
		}
	}

	for e := range tx.createLinks {
		if _, revoking := tx.revokeLinks[e]; revoking {
			return &StagingError{msg: fmt.Sprintf("link %s:%s->%s both created and revoked", e.linkType, e.src, e.dst)}
		}
		if _, creating := tx.createLinkTypes[e.linkType]; !creating {
			tl := s.linkTypes.get(e.linkType)
			if tl == nil {
				return &StagingError{msg: fmt.Sprintf("link %s references unknown link type", e.linkType)}
			}
			if _, ok := tl.at(prev); !ok {
				return &StagingError{msg: fmt.Sprintf("link %s references non-live link type", e.linkType)}
			}
		}
		for _, id := range []ObjectID{e.src, e.dst} {
			if _, creatingObj := tx.createObjects[id]; creatingObj {
				continue
			}
			tl := s.objects.get(id)
			if tl == nil {
				return &StagingError{msg: fmt.Sprintf("link endpoint %q does not exist", id)}
			}
			if o, ok := tl.at(prev); !ok || !o.alive {
				return &StagingError{msg: fmt.Sprintf("link endpoint %q is deleted at instant %d", id, prev)}
			}
		}
		if tl := s.edges.get(e); tl != nil {
			if r, ok := tl.at(prev); ok && r.alive {
				return &StagingError{msg: fmt.Sprintf("link %s:%s->%s already exists", e.linkType, e.src, e.dst)}
			}
		}
	}
	for e := range tx.revokeLinks {
		tl := s.edges.get(e)
		if tl == nil {
			return &StagingError{msg: fmt.Sprintf("cannot revoke never-created link %s:%s->%s", e.linkType, e.src, e.dst)}
		}
		if r, ok := tl.at(prev); !ok || !r.alive {
			return &StagingError{msg: fmt.Sprintf("cannot revoke non-live link %s:%s->%s", e.linkType, e.src, e.dst)}
		}
	}
	return nil
}

// validate enforces cardinality for staged link creations, counting the
// out-degree each source will have after this commit. Cardinality governs
// creation only: later tightening never removes already-existing links.
func (tx *Tx) validate(at Instant) error {
	type srcType struct {
		src ObjectID
		lt  LinkTypeID
	}
	degree := map[srcType]int{}

	addAlive := func(e edgeKey) {
		if _, drop := tx.revokeLinks[e]; drop {
			return
		}
		if _, gone := tx.deleteObjects[e.src]; gone {
			return
		}
		if _, gone := tx.deleteObjects[e.dst]; gone {
			return
		}
		degree[srcType{e.src, e.linkType}]++
	}

	s := tx.store
	// Commit already serializes writers; a read lock here would re-enter the
	// same RWMutex the publish phase needs for writing and deadlock.
	s.edges.rangeWriteLocked(func(key edgeKey, tl *timeline[edgeRecord]) {
		if e, ok := tl.at(at - 1); ok && e.alive {
			addAlive(key)
		}
	})
	for e := range tx.createLinks {
		addAlive(e)
	}

	seen := map[edgeKey]struct{}{}
	for e := range tx.createLinks {
		if _, drop := tx.revokeLinks[e]; drop {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		card := tx.cardAfter(at, e.linkType)
		if card.MaxOut >= 0 && degree[srcType{e.src, e.linkType}] > card.MaxOut {
			return &StagingError{msg: fmt.Sprintf(
				"creating link %s: %s->%s violates cardinality MaxOut=%d at instant %d",
				e.linkType, e.src, e.dst, card.MaxOut, at)}
		}
	}
	return nil
}

// cardAfter returns the cardinality of lt visible after this commit.
func (tx *Tx) cardAfter(at Instant, lt LinkTypeID) Cardinality {
	if c, ok := tx.createLinkTypes[lt]; ok {
		return c
	}
	if c, ok := tx.adjustCards[lt]; ok {
		return c
	}
	if tl := tx.store.linkTypes.get(lt); tl != nil {
		if e, ok := tl.at(at - 1); ok {
			return e.value
		}
	}
	return Cardinality{MaxOut: -1}
}

// cascadeDeletes revokes every incident link of each deleted object.
func (tx *Tx) cascadeDeletes(at Instant) {
	s := tx.store
	for id := range tx.deleteObjects {
		if out := s.adjOut.get(id); out != nil {
			if root := rootAt(out.load(), at-1); root != nil {
				treapIter(root, func(k treapKey) bool {
					tx.revokeLinks[edgeKey{linkType: k.linkType, src: id, dst: k.other}] = struct{}{}
					return true
				})
			}
		}
		if in := s.adjIn.get(id); in != nil {
			if root := rootAt(in.load(), at-1); root != nil {
				treapIter(root, func(k treapKey) bool {
					tx.revokeLinks[edgeKey{linkType: k.linkType, src: k.other, dst: id}] = struct{}{}
					return true
				})
			}
		}
	}
}

// applyTypes publishes object-type and link-type definition versions.
func (tx *Tx) applyTypes(at Instant) {
	s := tx.store
	for id, props := range tx.createObjectTypes {
		s.objectTypes.getOrCreateLocked(id).appendLocked(entry[[]Property]{at: at, value: props, alive: true})
	}
	for id, props := range tx.migrateTypes {
		s.objectTypes.getOrCreateLocked(id).appendLocked(entry[[]Property]{at: at, value: props, alive: true})
	}
	for id, card := range tx.createLinkTypes {
		s.linkTypes.getOrCreateLocked(id).appendLocked(entry[Cardinality]{at: at, value: card, alive: true})
	}
	for id, card := range tx.adjustCards {
		s.linkTypes.getOrCreateLocked(id).appendLocked(entry[Cardinality]{at: at, value: card, alive: true})
	}
}

// applyObjects publishes object creates, property updates and deletions.
func (tx *Tx) applyObjects(at Instant) {
	s := tx.store
	for id, rec := range tx.createObjects {
		if extra, ok := tx.setProps[id]; ok {
			merged := cloneProps(rec.props)
			for k, v := range extra {
				merged[k] = v
			}
			rec.props = merged
		}
		s.objects.getOrCreateLocked(id).appendLocked(entry[objectRecord]{at: at, value: rec, alive: true})
	}
	for id, props := range tx.setProps {
		if _, creating := tx.createObjects[id]; creating {
			continue
		}
		tl := s.objects.getOrCreateLocked(id)
		cur, _ := tl.at(at - 1)
		merged := cloneProps(cur.value.props)
		for k, v := range props {
			merged[k] = v
		}
		tl.appendLocked(entry[objectRecord]{
			at:    at,
			value: objectRecord{typeID: cur.value.typeID, props: merged},
			alive: true,
		})
	}
	for id := range tx.deleteObjects {
		tl := s.objects.getOrCreateLocked(id)
		cur, _ := tl.at(at - 1)
		tl.appendLocked(entry[objectRecord]{at: at, value: cur.value, alive: false})
	}
}

// applyEdges publishes link creates/revokes on edge timelines and patches the
// persistent adjacency treaps of both endpoints.
func (tx *Tx) applyEdges(at Instant) {
	s := tx.store

	type delta struct {
		key   edgeKey
		alive bool
	}
	var deltas []delta

	for e := range tx.createLinks {
		if _, drop := tx.revokeLinks[e]; !drop {
			deltas = append(deltas, delta{e, true})
		}
	}
	for e := range tx.revokeLinks {
		if _, create := tx.createLinks[e]; !create {
			deltas = append(deltas, delta{e, false})
		}
	}

	// Sort deltas for deterministic application.
	sort.Slice(deltas, func(i, j int) bool {
		x, y := deltas[i].key, deltas[j].key
		if x.src != y.src {
			return x.src < y.src
		}
		if x.dst != y.dst {
			return x.dst < y.dst
		}
		return x.linkType < y.linkType
	})

	// Each adjacency bucket must publish exactly one new root per commit, so
	// all edges in this transaction sharing a source (or destination) are
	// folded into one treap update. Publishing one root per edge would let
	// same-instant roots overwrite one another.
	outFinal := map[ObjectID]*treapNode{}
	inFinal := map[ObjectID]*treapNode{}

	edgeTimeline := func(e edgeKey, alive bool) {
		s.edges.getOrCreateLocked(e).appendLocked(entry[edgeRecord]{at: at, alive: alive})
	}

	for _, d := range deltas {
		edgeTimeline(d.key, d.alive)
		// Accumulate from outFinal (this commit's running root), falling back
		// to the previous-instant root the first time this bucket is touched.
		root, seen := outFinal[d.key.src]
		if !seen {
			if bucket := s.adjOut.get(d.key.src); bucket != nil {
				root = rootAt(bucket.load(), at-1)
			}
		}
		if d.alive {
			root = treapInsert(root, treapKey{linkType: d.key.linkType, other: d.key.dst})
		} else {
			root = treapDelete(root, treapKey{linkType: d.key.linkType, other: d.key.dst})
		}
		outFinal[d.key.src] = root

		root, seen = inFinal[d.key.dst]
		if !seen {
			if bucket := s.adjIn.get(d.key.dst); bucket != nil {
				root = rootAt(bucket.load(), at-1)
			}
		}
		if d.alive {
			root = treapInsert(root, treapKey{linkType: d.key.linkType, other: d.key.src})
		} else {
			root = treapDelete(root, treapKey{linkType: d.key.linkType, other: d.key.src})
		}
		inFinal[d.key.dst] = root
	}

	// Publish one root per touched bucket.
	for id, root := range outFinal {
		bucket := s.adjOut.getOrCreateLocked(id)
		bucket.store(appendRoot(bucket.load(), adjRoot{at: at, root: root}))
	}
	for id, root := range inFinal {
		bucket := s.adjIn.getOrCreateLocked(id)
		bucket.store(appendRoot(bucket.load(), adjRoot{at: at, root: root}))
	}
}

func appendRoot(roots []adjRoot, r adjRoot) []adjRoot {
	next := make([]adjRoot, len(roots)+1)
	copy(next, roots)
	next[len(roots)] = r
	return next
}

// rootAt selects the adjacency root in force at instant t from a bucket's
// version sequence. Returns nil when the bucket had no edges at or before t.
func rootAt(roots []adjRoot, t Instant) *treapNode {
	lo, hi := 0, len(roots)
	for lo < hi {
		mid := (lo + hi) / 2
		if roots[mid].at > t {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == 0 {
		return nil
	}
	return roots[lo-1].root
}

// buildEntry snapshots the staged change set for the commit log / oracle.
func (tx *Tx) buildEntry(at Instant) CommitEntry {
	deleted := make([]ObjectID, 0, len(tx.deleteObjects))
	for id := range tx.deleteObjects {
		deleted = append(deleted, id)
	}
	creates := make([]edgeKey, 0, len(tx.createLinks))
	revokes := make([]edgeKey, 0, len(tx.revokeLinks))
	for e := range tx.createLinks {
		if _, drop := tx.revokeLinks[e]; !drop {
			creates = append(creates, e)
		}
	}
	for e := range tx.revokeLinks {
		if _, alsoCreate := tx.createLinks[e]; !alsoCreate {
			revokes = append(revokes, e)
		}
	}
	createObjs := make(map[ObjectID]objectRecord, len(tx.createObjects))
	for id, rec := range tx.createObjects {
		createObjs[id] = rec
	}
	setProps := make(map[ObjectID]PropertyValues, len(tx.setProps))
	for id, p := range tx.setProps {
		setProps[id] = p
	}
	return CommitEntry{
		At:                at,
		CreateObjectTypes: tx.createObjectTypes,
		MigrateTypes:      tx.migrateTypes,
		CreateLinkTypes:   tx.createLinkTypes,
		AdjustCards:       tx.adjustCards,
		CreateObjects:     createObjs,
		SetProps:          setProps,
		DeleteObjects:     deleted,
		CreateLinks:       creates,
		RevokeLinks:       revokes,
	}
}
