package ontology

import "fmt"

// Snapshot is a pinned, immutable view of one config version. It must be
// released when the operation using it finishes. Release is idempotent.
type Snapshot struct {
	m        *Manager
	version  int64
	released bool
}

// Version returns the version number this snapshot points to. It stays
// valid even after the snapshot is released.
func (s *Snapshot) Version() int64 {
	return s.version
}

// checkLocked verifies the snapshot is still usable. Caller must hold m.mu.
// A reclaimed version is reported before a release, so a snapshot whose
// version is gone always yields ErrVersionReclaimed.
func (s *Snapshot) checkLocked() error {
	if _, ok := s.m.versions[s.version]; !ok {
		return fmt.Errorf("%w: %d", ErrVersionReclaimed, s.version)
	}
	if s.released {
		return ErrSnapshotReleased
	}
	return nil
}

// Get returns the value of a declared field as of this snapshot's version.
// The value never changes, no matter how many updates happen afterwards.
func (s *Snapshot) Get(name string) (any, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if err := s.checkLocked(); err != nil {
		return nil, err
	}
	v, ok := s.m.versions[s.version].values[name]
	if !ok {
		return nil, &ValidationError{Field: name, Kind: KindUnknownField}
	}
	return v, nil
}

// SourceVersion returns the version number of the update that last wrote
// the given field, as seen by this snapshot.
func (s *Snapshot) SourceVersion(name string) (int64, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if err := s.checkLocked(); err != nil {
		return 0, err
	}
	v, ok := s.m.versions[s.version].sources[name]
	if !ok {
		return 0, &ValidationError{Field: name, Kind: KindUnknownField}
	}
	return v, nil
}

// Release returns the snapshot's reference. It is idempotent: calling it
// more than once is a harmless no-op and never drives the reference count
// negative.
func (s *Snapshot) Release() {
	s.m.release(s)
}
