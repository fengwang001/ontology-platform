package ontology

import (
	"fmt"
	"sync"
)

// NaiveStore is an independently written reference implementation: every
// operation takes one global lock and adjudicates a write by scanning every
// committed version since the baseline. It is intentionally simple and
// O(history); randomized tests compare the concurrent Store against it under
// an equivalent serial order.
type NaiveStore struct {
	mu    sync.Mutex
	types map[TypeName][]Validator
	inst  map[ObjectID]*naiveInst
	idem  map[string]struct{}
	clock uint64
}

type naiveInst struct {
	exists   bool
	versions []*snapshot
	// record[i] is the relevant set footprint of version i+1.
	footprint []naiveFoot
}

type naiveFoot struct {
	local    map[Property]bool
	ext      map[ObjectID]map[Property]bool
	observed map[ObjectID]Version
}

func NewNaiveStore() *NaiveStore {
	return &NaiveStore{types: map[TypeName][]Validator{}, inst: map[ObjectID]*naiveInst{}, idem: map[string]struct{}{}}
}

func (n *NaiveStore) RegisterType(t TypeName, vs ...Validator) {
	n.mu.Lock()
	n.types[t] = append(append([]Validator(nil), n.types[t]...), vs...)
	n.mu.Unlock()
}

func (n *NaiveStore) Head(id ObjectID) (Version, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.inst[id]
	if st == nil || !st.exists {
		return 0, false
	}
	return Version(len(st.versions)), true
}

func (n *NaiveStore) Read(id ObjectID, v Version) (Snapshot, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	st := n.inst[id]
	if st == nil || !st.exists || v <= 0 || int(v) > len(st.versions) {
		return nil, false
	}
	return st.versions[v-1], true
}

func (n *NaiveStore) Clock() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.clock
}

// SerialCommit runs one request as if at the end of a serial schedule. It
// resolves hook scopes and adjudicates purely from committed history.
func (n *NaiveStore) SerialCommit(req WriteRequest) Result {
	n.mu.Lock()
	defer n.mu.Unlock()

	cur := n.inst[req.Object]
	var baseSnap *snapshot
	if cur != nil && req.Base > 0 && int(req.Base) <= len(cur.versions) {
		baseSnap = cur.versions[req.Base-1]
	}
	values := cloneValues(req.Values)
	if req.Delete {
		values = map[Property]Value{}
	}

	// Hook scope declared on the baseline snapshot.
	local := map[Property]bool{}
	ext := map[ObjectID]map[Property]bool{}
	observed := map[ObjectID]Version{}
	for id, v := range req.ObserveExternal {
		observed[id] = v
	}
	for _, h := range n.types[req.Type] {
		if h.Declare == nil {
			continue
		}
		var view Snapshot
		if baseSnap != nil {
			view = baseSnap
		}
		scope := h.Declare(values, view)
		for _, ref := range scope.Refs {
			target := ref.Instance
			if target == "" && ref.Link != "" {
				target = resolveLink(baseSnap, ref.Link)
			}
			if ref.Link == "" {
				local[ref.Local] = true
			} else {
				m := ext[target]
				if m == nil {
					m = map[Property]bool{}
					ext[target] = m
				}
				m[ref.Local] = true
			}
			if target != "" {
				if _, ok := observed[target]; !ok {
					if o := n.inst[target]; o != nil && o.exists {
						observed[target] = Version(len(o.versions))
					} else {
						observed[target] = 0
					}
				}
			}
		}
		for id, v := range scope.ExternalVersions {
			if cur, ok := observed[id]; !ok || v > cur {
				observed[id] = v
			}
		}
	}
	for k := range values {
		local[k] = true
	}
	if req.Delete {
		local[deleteSentinel] = true
	}

	// Validation.
	for _, h := range n.types[req.Type] {
		if h.Validate == nil {
			continue
		}
		linked := map[ObjectID]Snapshot{}
		for id := range observed {
			if o := n.inst[id]; o != nil && o.exists {
				linked[id] = o.versions[len(o.versions)-1]
			}
		}
		var view Snapshot
		if cur != nil && cur.exists {
			view = cur.versions[len(cur.versions)-1]
		}
		if err := h.Validate(values, view, linked); err != nil {
			return Result{Err: fmt.Errorf("%w: %v", ErrRejected, err)}
		}
	}

	// Adjudication: deleted first.
	if cur != nil && cur.exists && cur.versions[len(cur.versions)-1].deleted {
		return Result{Kind: ConflictDeleted, Err: fmt.Errorf("ontology: %w", ErrInstanceDeleted)}
	}
	if req.IdempotencyKey != "" {
		if _, ok := n.idem[string(req.Object)+"\x00"+req.IdempotencyKey]; ok {
			return Result{Kind: ConflictStaleBase, Err: fmt.Errorf("ontology: %w: duplicate", ErrStaleBase)}
		}
	}
	if req.Create {
		if cur != nil && cur.exists {
			return Result{Kind: ConflictStaleBase, Err: fmt.Errorf("ontology: %w: create", ErrStaleBase)}
		}
	} else if cur == nil || !cur.exists {
		return Result{Kind: ConflictStaleBase, Err: fmt.Errorf("ontology: %w: unknown", ErrStaleBase)}
	} else if req.Base <= 0 || int(req.Base) > len(cur.versions) {
		return Result{Kind: ConflictStaleBase, Err: fmt.Errorf("ontology: %w: bad base", ErrStaleBase)}
	}

	// Scan every footprint committed since baseline (the intentionally
	// expensive, obviously-correct rule).
	if cur != nil {
		for i := int(req.Base); i < len(cur.footprint); i++ {
			f := cur.footprint[i]
			if intersects(local, f.local) {
				return Result{Kind: ConflictProperty, Err: fmt.Errorf("ontology: %w", ErrPropertyConflict)}
			}
			// Earlier write read linked props that this write now touches, or
			// vice versa: relevant-set intersection in either direction.
			for id, ps := range ext {
				if fp, ok := f.ext[id]; ok && intersects(ps, fp) {
					return Result{Kind: ConflictProperty, Err: fmt.Errorf("ontology: %w", ErrPropertyConflict)}
				}
			}
			for id, fext := range f.ext {
				if _, touched := ext[id]; touched {
					if intersects(fext, extPropsOf(ext, id)) {
						return Result{Kind: ConflictProperty, Err: fmt.Errorf("ontology: %w", ErrPropertyConflict)}
					}
				}
			}
		}
	}
	for id, ps := range ext {
		other := n.inst[id]
		obs := observed[id]
		if other == nil || !other.exists {
			continue
		}
		for i := int(obs); i < len(other.footprint); i++ {
			if other.footprint[i].localAny(ps) {
				return Result{Kind: ConflictProperty, Err: fmt.Errorf("ontology: %w", ErrPropertyConflict)}
			}
		}
	}

	// Apply.
	if cur == nil {
		cur = &naiveInst{}
		n.inst[req.Object] = cur
	}
	var props []snapshotProp
	if req.Create || req.Base == 0 {
		props = mergeProps(nil, values)
	} else {
		props = mergeProps(cur.versions[len(cur.versions)-1].props, values)
	}
	newVer := Version(len(cur.versions) + 1)
	cur.versions = append(cur.versions, encodeSnapshot(newVer, req.Delete, props))
	cur.exists = true
	cur.footprint = append(cur.footprint, naiveFoot{local: local, ext: ext, observed: observed})
	n.clock++
	if req.IdempotencyKey != "" {
		n.idem[string(req.Object)+"\x00"+req.IdempotencyKey] = struct{}{}
	}
	return Result{NewVersion: newVer}
}

func intersectsLocal(a, b map[Property]bool) bool {
	// Deletes touch the delete sentinel only; two non-delete writes compare
	// property sets normally.
	return intersects(a, b)
}

func intersects(a, b map[Property]bool) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func (f naiveFoot) localAny(ps map[Property]bool) bool {
	return intersects(f.local, ps)
}

func extPropsOf(ext map[ObjectID]map[Property]bool, id ObjectID) map[Property]bool {
	if ps, ok := ext[id]; ok {
		return ps
	}
	return map[Property]bool{}
}
