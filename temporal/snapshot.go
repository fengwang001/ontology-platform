package temporal

import (
	"fmt"
	"sync/atomic"
)

// Snapshot is an immutable read-only view of the whole ontology at one fixed
// instant. It is safe for concurrent use and unaffected by commits that land
// while a traversal is running: every timeline is a copy-on-write slice and
// every adjacency bucket is a sequence of persistent treap roots, so the
// pointers captured here keep resolving to the same historical versions.
type Snapshot struct {
	store *Store
	at    Instant

	gaps []Gap // frozen when the snapshot is pinned

	// Probe counters backing the independently verifiable cost proof.
	timelineProbes  atomic.Int64
	adjacencyProbes atomic.Int64
}

// Snapshot pins an immutable read view at instant t. It fails with
// ErrBeforeHorizon when t predates the replay horizon; requesting a future
// instant is also an error rather than silently clamping to head.
func (s *Store) Snapshot(t Instant) (*Snapshot, error) {
	if t < s.horizon.get() {
		return nil, &TraversalError{
			Code:   ErrBeforeHorizon,
			At:     t,
			Detail: fmt.Sprintf("instant %d precedes replay horizon %d", t, s.horizon.get()),
		}
	}
	if t > s.head.get() {
		return nil, &TraversalError{
			Code:   ErrMissingHistory,
			At:     t,
			Detail: fmt.Sprintf("instant %d is in the future (head=%d)", t, s.head.get()),
		}
	}
	return &Snapshot{store: s, at: t, gaps: s.snapshotGaps()}, nil
}

// At returns the instant this snapshot is anchored at.
func (sn *Snapshot) At() Instant { return sn.at }

// ObjectState is an object's exact state at the snapshot instant.
type ObjectState struct {
	ID         ObjectID
	Type       TypeID
	Exists     bool
	Properties PropertyValues // interpreted under the type version at At
}

// Link is one edge existing at the snapshot instant.
type Link struct {
	Type LinkTypeID
	Src  ObjectID
	Dst  ObjectID
}

// missingf builds an ErrMissingHistory error for one decision.
func (sn *Snapshot) missingf(format string, args ...any) error {
	return &TraversalError{Code: ErrMissingHistory, At: sn.at, Detail: fmt.Sprintf(format, args...)}
}

// gapHit reports whether any frozen gap covers (kind,key) at the pinned
// instant.
func (sn *Snapshot) gapHit(kind GapKind, key string) bool {
	for i := range sn.gaps {
		if sn.gaps[i].covers(sn.at, kind, key) {
			return true
		}
	}
	return false
}

// Object returns the object state at the pinned instant. A non-existent
// (or already-deleted) object returns Exists=false without error; only a
// declared history gap is an error.
func (sn *Snapshot) Object(id ObjectID) (ObjectState, error) {
	if sn.gapHit(GapObject, string(id)) {
		return ObjectState{}, sn.missingf("object %q timeline has a history gap at %d", id, sn.at)
	}
	tl := sn.store.objects.get(id)
	sn.timelineProbes.Add(1)
	if tl == nil {
		return ObjectState{ID: id}, nil
	}
	e, ok := tl.at(sn.at)
	if !ok || !e.alive {
		return ObjectState{ID: id}, nil
	}
	return ObjectState{
		ID:         id,
		Type:       e.value.typeID,
		Exists:     true,
		Properties: cloneProps(e.value.props),
	}, nil
}

// ObjectTypeProps returns the property definition in force exactly at At.
// The returned slice is a defensive copy.
func (sn *Snapshot) ObjectTypeProps(id TypeID) ([]Property, bool, error) {
	if sn.gapHit(GapObjectType, string(id)) {
		return nil, false, sn.missingf("object type %q has a history gap at %d", id, sn.at)
	}
	tl := sn.store.objectTypes.get(id)
	sn.timelineProbes.Add(1)
	if tl == nil {
		return nil, false, nil
	}
	e, ok := tl.at(sn.at)
	if !ok || !e.alive {
		return nil, false, nil
	}
	return append([]Property(nil), e.value...), true, nil
}

// Cardinality returns the link cardinality rule in force exactly at At — the
// unique version whose half-open [from,to) interval covers At, never "the
// nearest" version.
func (sn *Snapshot) Cardinality(id LinkTypeID) (Cardinality, bool, error) {
	if sn.gapHit(GapLinkType, string(id)) {
		return Cardinality{}, false, sn.missingf("link type %q has a history gap at %d", id, sn.at)
	}
	tl := sn.store.linkTypes.get(id)
	sn.timelineProbes.Add(1)
	if tl == nil {
		return Cardinality{}, false, nil
	}
	e, ok := tl.at(sn.at)
	if !ok || !e.alive {
		return Cardinality{}, false, nil
	}
	return e.value, true, nil
}

// edgeAlive checks one edge instance's own lifecycle timeline. Its cost is a
// single binary search over only that edge's create/revoke entries: it does
// not scan, and does not grow with, the link type's total history.
func (sn *Snapshot) edgeAlive(e edgeKey) (bool, error) {
	if sn.gapHit(GapEdge, "") {
		return false, sn.missingf("edge %s:%s->%s has a history gap at %d", e.linkType, e.src, e.dst, sn.at)
	}
	tl := sn.store.edges.get(e)
	sn.timelineProbes.Add(1)
	if tl == nil {
		return false, nil
	}
	r, ok := tl.at(sn.at)
	return ok && r.alive, nil
}

// LinkExists reports whether one specific link existed at At. It uses the
// adjacency index (one root version lookup + one O(log k) treap probe) and
// cross-confirms with the edge lifecycle timeline; both are independent of
// the number of events ever recorded for the link type.
func (sn *Snapshot) LinkExists(linkType LinkTypeID, src, dst ObjectID) (bool, error) {
	root, err := sn.outRoot(src)
	if err != nil {
		return false, err
	}
	sn.adjacencyProbes.Add(1)
	inIndex := treapContains(root, treapKey{linkType: linkType, other: dst})
	alive, err := sn.edgeAlive(edgeKey{linkType: linkType, src: src, dst: dst})
	if err != nil {
		return false, err
	}
	if inIndex != alive {
		// Internal indexes must agree; a disagreement means history is
		// corrupt, which is a missing-history failure, not a guess.
		return false, sn.missingf("index disagreement for %s:%s->%s at %d", linkType, src, dst, sn.at)
	}
	return alive, nil
}

// outRoot returns src's outgoing-adjacency treap root in force at At.
func (sn *Snapshot) outRoot(src ObjectID) (*treapNode, error) {
	if sn.gapHit(GapAdjacency, string(src)) {
		return nil, sn.missingf("adjacency of %q has a history gap at %d", src, sn.at)
	}
	bucket := sn.store.adjOut.get(src)
	sn.adjacencyProbes.Add(1)
	if bucket == nil {
		return nil, nil
	}
	return rootAt(bucket.load(), sn.at), nil
}

// LinksFrom enumerates outgoing links of src, optionally filtered by link
// type (empty LinkTypeID means all). Every enumerated key is confirmed
// against the edge lifecycle timeline; enumeration stops if fn returns false.
func (sn *Snapshot) LinksFrom(src ObjectID, only LinkTypeID, fn func(Link) bool) error {
	root, err := sn.outRoot(src)
	if err != nil {
		return err
	}
	var keys []treapKey
	treapIter(root, func(k treapKey) bool {
		if only != "" && k.linkType != only {
			return true
		}
		keys = append(keys, k)
		return true
	})
	// keys come out in deterministic (linkType, other) order, so traversals
	// are reproducible across runs and against the naive oracle.
	for _, k := range keys {
		alive, err := sn.edgeAlive(edgeKey{linkType: k.linkType, src: src, dst: k.other})
		if err != nil {
			return err
		}
		if !alive {
			return sn.missingf("index/edge disagreement for %s:%s->%s at %d",
				k.linkType, src, k.other, sn.at)
		}
		if !fn(Link{Type: k.linkType, Src: src, Dst: k.other}) {
			// A false return is a normal "stop enumerating"; any error the
			// callback stashed in its closure is the caller's responsibility
			// and must not be masked here.
			return callbackStopped{}
		}
	}
	return nil
}

// callbackStopped is a sentinel distinguishing a normal callback stop from a
// real lookup failure. It is never observed by callers because it is returned
// only after the callback itself already decided to stop.
type callbackStopped struct{}

func (callbackStopped) Error() string { return "enumeration stopped by callback" }

// AccessStats counts low-level history probes; it backs the independently
// verifiable per-decision cost proof.
type AccessStats struct {
	TimelineProbes  int
	AdjacencyProbes int
}

// Stats returns and resets the probe counters.
func (sn *Snapshot) Stats() AccessStats {
	return AccessStats{
		TimelineProbes:  int(sn.timelineProbes.Swap(0)),
		AdjacencyProbes: int(sn.adjacencyProbes.Swap(0)),
	}
}

// allEdgesAt is a test/verification helper: it enumerates every edge alive at
// the pinned instant by walking the edge timeline family. It is O(total edge
// instances) by design and is never used on the traversal hot path; tests use
// it to pick random edges to revoke.
func (sn *Snapshot) allEdgesAt(fn func(edgeKey)) {
	sn.store.edges.rangeLocked(func(k edgeKey, tl *timeline[edgeRecord]) {
		if r, ok := tl.at(sn.at); ok && r.alive {
			fn(k)
		}
	})
}
