package percolator

import "sort"

// KeySnapshot is the deterministic per-key view used in tests.
type KeySnapshot struct {
	Versions []Version
	Lock     *Lock
}

// Snapshot is a full deterministic copy of store state. Equal snapshots
// mean identical version tables, locks, oracle and clock water mark.
type Snapshot struct {
	Oracle int64
	Water  int64
	Keys   map[string]KeySnapshot
}

// Snapshot returns a deep, key-sorted copy of the store state.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make(map[string]KeySnapshot, len(s.data.keys))
	for name, k := range s.data.keys {
		snap := KeySnapshot{Versions: append([]Version(nil), k.versions...)}
		if k.lock != nil {
			lk := *k.lock
			snap.Lock = &lk
		}
		keys[name] = snap
	}
	return Snapshot{Oracle: s.data.oracle, Water: s.data.water, Keys: keys}
}

// SortedKeys returns the snapshot's keys in stable order.
func (snap Snapshot) SortedKeys() []string {
	out := make([]string, 0, len(snap.Keys))
	for k := range snap.Keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
