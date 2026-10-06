package layerconfig

import (
	"sync"
	"sync/atomic"
)

// snapshot is the immutable header published atomically on every successful
// publication or rollback. States inside it are persistent and never mutate,
// so a reader that has snapshotted the header resolves a consistent version
// with no possibility of observing a half-applied publication.
type snapshot struct {
	version int
	current *state
	history []*state
}

// Store is the concurrency-safe, linearizable facade over the configuration.
//
// All mutating operations (RegisterKey, Publish, Rollback) take one global
// mutex, so they have a well-defined total order. Readers never block: they
// snapshot the current immutable state pointer and resolve against it, which
// makes it impossible to observe a half-applied publication.
type Store struct {
	// mu serializes all mutations and protects the append of the history
	// slice. Readers take RLock, so concurrent resolves run in parallel; a
	// publishing writer is exclusive.
	mu sync.RWMutex

	reg *registry

	// head is the atomically published current snapshot. It is also read
	// under RLock for convenience; atomicity makes the pointer publication
	// explicitly total (synchronizes-with the writer's unlock).
	head atomic.Pointer[snapshot]
}

// NewStore creates an empty store at version 0 with empty configuration.
func NewStore() *Store {
	s := &Store{reg: newRegistry()}
	initial := initialState()
	s.head.Store(&snapshot{
		version: 0,
		current: initial,
		history: []*state{initial},
	})
	return s
}

// RegisterKey creates or replaces the latest schema of a key. Schemas are not
// versioned; historical reads always use the newest schema. It is serialized
// with publications so a read can never apply a schema mid-publication.
func (s *Store) RegisterKey(sc Schema) error {
	if err := s.reg.validateSchema(sc); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reg.register(sc)
}

// Publish applies an all-or-nothing batch and returns the new version number.
// On failure no state or version changes.
func (s *Store) Publish(changes []Change) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	head := s.head.Load()
	base := head.current
	candidate, err := applyBatch(base, s.reg, changes,
		func(st *state, target Scope, key string) (Resolved, error) {
			return resolveKey(st, s.reg, target, key)
		})
	if err != nil {
		return head.version, err
	}
	history := append(append([]*state(nil), head.history...), candidate)
	s.head.Store(&snapshot{version: candidate.version, current: candidate, history: history})
	return candidate.version, nil
}

// Rollback creates a new version whose full content equals the target
// historical version. The target and all earlier versions are retained.
// Rolling back to the current version is a no-op and allocates no new
// version. The resulting state must satisfy required validation under the
// current (latest) schemas; only a later schema change can make a previously
// valid target invalid.
func (s *Store) Rollback(targetVersion int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	head := s.head.Load()
	if targetVersion < 0 {
		return head.version, errInvalid("rollback target version must be >= 0, got %d", targetVersion)
	}
	if targetVersion >= len(head.history) {
		return head.version, errVersion("rollback target version %d does not exist (current=%d)",
			targetVersion, head.version)
	}
	target := head.history[targetVersion]
	if targetVersion == head.version {
		return head.version, nil
	}
	clone := &state{
		version: head.version + 1,
		writes:  target.writes,
		locks:   target.locks,
		exists:  target.exists,
	}
	if err := validateRequired(clone, s.reg,
		func(st *state, t Scope, key string) (Resolved, error) {
			return resolveKey(st, s.reg, t, key)
		}); err != nil {
		return head.version, err
	}
	history := append(append([]*state(nil), head.history...), clone)
	s.head.Store(&snapshot{version: clone.version, current: clone, history: history})
	return clone.version, nil
}

// snapshotAt returns the immutable state for the requested version. version
// 0 is the initial empty state; a negative value means current.
func (s *Store) snapshotAt(version int) (*state, error) {
	head := s.head.Load()
	if version == 0 {
		// Version 0 is always the first element, even after rollback.
		return head.history[0], nil
	}
	if version < 0 {
		return head.current, nil
	}
	if version >= len(head.history) {
		return nil, errVersion("version %d does not exist (current=%d)", version, head.version)
	}
	return head.history[version], nil
}

// Resolve resolves one key at a historical version. Pass 0 (or a negative
// value) for the current version.
func (s *Store) Resolve(version int, target Scope, key string) (Resolved, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := applicableScopes(target); err != nil {
		return Unset, err
	}
	if key == "" {
		return Unset, errInvalid("resolve key must not be empty")
	}
	st, err := s.snapshotAt(version)
	if err != nil {
		return Unset, err
	}
	if _, ok := s.reg.get(key); !ok {
		return Unset, errKey(key)
	}
	return resolveKey(st, s.reg, target, key)
}

// ResolveView resolves multiple keys at one version, returning one Resolved
// per requested key in order.
func (s *Store) ResolveView(version int, target Scope, keys []string) ([]Resolved, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := applicableScopes(target); err != nil {
		return nil, err
	}
	for i, key := range keys {
		if key == "" {
			return nil, errInvalid("resolve key at index %d must not be empty", i)
		}
	}
	st, err := s.snapshotAt(version)
	if err != nil {
		return nil, err
	}
	out := make([]Resolved, len(keys))
	for i, key := range keys {
		if _, ok := s.reg.get(key); !ok {
			return nil, errKey(key)
		}
		r, err := resolveKey(st, s.reg, target, key)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// CurrentVersion returns the current version number.
func (s *Store) CurrentVersion() int {
	return s.head.Load().version
}
