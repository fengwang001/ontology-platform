package ontology

import "slices"

// Snapshot is a canonical, comparable view of the engine's observable
// state: live objects, live links, per-instance link counts and the
// logical clock.
type Snapshot struct {
	Objects map[ObjectID]ObjectTypeID
	Links   []LinkRef
	Counts  map[ObjectID]map[LinkTypeID]int
	Clock   uint64
}

// Snapshot captures the current state. It is safe to call concurrently
// with operations.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) snapshotLocked() Snapshot {
	snap := Snapshot{
		Objects: make(map[ObjectID]ObjectTypeID, len(e.objects)),
		Counts:  make(map[ObjectID]map[LinkTypeID]int, len(e.counts)),
		Clock:   e.clock,
	}
	for id, typ := range e.objects {
		snap.Objects[id] = typ
	}
	for k := range e.links {
		snap.Links = append(snap.Links, k.ref())
	}
	slices.SortFunc(snap.Links, func(a, b LinkRef) int {
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
	for o, m := range e.counts {
		if len(m) == 0 {
			continue
		}
		cm := make(map[LinkTypeID]int, len(m))
		for lt, c := range m {
			cm[lt] = c
		}
		snap.Counts[o] = cm
	}
	return snap
}
