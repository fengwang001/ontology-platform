package percolator

// Snapshot is a deterministic read-only view of store internals, used for
// tests and verification.
type Snapshot struct {
	Oracle    uint64
	Watermark uint64
	Versions  map[string][]Version
	Locks     map[string]Lock
}

// Snapshot returns deep copies of every version table and lock.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{
		Oracle:    s.oracle,
		Watermark: s.watermark,
		Versions:  map[string][]Version{},
		Locks:     map[string]Lock{},
	}
	for k, ks := range s.keys {
		if len(ks.versions) > 0 {
			vs := make([]Version, len(ks.versions))
			copy(vs, ks.versions)
			snap.Versions[k] = vs
		}
		if ks.lock != nil {
			snap.Locks[k] = *ks.lock
		}
	}
	return snap
}

// VersionsOf returns a copy of the key's committed version table.
func (s *Store) VersionsOf(key string) []Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil || len(ks.versions) == 0 {
		return nil
	}
	vs := make([]Version, len(ks.versions))
	copy(vs, ks.versions)
	return vs
}

// LockOf returns a copy of the key's lock and whether one exists.
func (s *Store) LockOf(key string) (Lock, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil || ks.lock == nil {
		return Lock{}, false
	}
	return *ks.lock, true
}
