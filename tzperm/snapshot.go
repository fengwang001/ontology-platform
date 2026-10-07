package tzperm

// Snapshot is the immutable state one Check evaluates against. Slices are
// shared by reference with the store and never mutated after publication;
// mutators replace whole slices instead of editing them in place.
type Snapshot struct {
	Regions    map[string][]ZoneVersion
	WindowSets map[string][]WindowVersion
	Types      map[string][]TypeVersion
	Policies   map[policyKey]policy
	Objects    map[string]Object
}

// snapshotLocked must be called while holding the store lock.
func (s *Store) snapshotLocked() Snapshot {
	return Snapshot{
		Regions:    s.regions,
		WindowSets: s.windowSets,
		Types:      s.types,
		Policies:   s.policies,
		Objects:    s.objects,
	}
}

// Snapshot returns a state snapshot while holding the read lock. It is
// exported for external verification tools and the naive cross-check model.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}
