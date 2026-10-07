package ontology

import (
	"sync"
	"sync/atomic"
)

// Store is the object instance store. Concurrency control is property
// level: concurrent writes with disjoint relevant sets (write set union the
// read scopes of registered validation hooks) both succeed and merge onto
// the same baseline; intersecting relevant sets conflict.
//
// Per-instance mutexes (taken in sorted ObjectID order) make every commit
// linearizable; a store-wide logical clock ticks exactly once per accepted
// write and is observable evidence distinguishing accepted from rejected
// paths. The clock never feeds version assignment.
type Store struct {
	resolveMu sync.Mutex

	typeMu sync.RWMutex
	types  map[TypeName][]Validator

	objMu sync.Mutex
	obj   map[ObjectID]*objState

	idemMu sync.Mutex
	idem   map[string]idemRecord

	clock atomic.Uint64
}

type idemRecord struct {
	kind    ConflictKind
	version Version
}

// objState is the mutable state of one instance. Its versions slice holds
// immutable snapshots indexed by Version (slot 0 is nil: Version 0 is the
// pre-create baseline).
type objState struct {
	mu sync.Mutex

	exists bool
	head   Version

	versions  []*snapshot
	decisions []Decision

	// Conflict markers: latest Version whose relevant set touched a key.
	// Adjudication is "marker > baseline", so its cost depends only on the
	// incoming relevant-set size, never on len(versions).
	localMark map[Property]Version
}

// NewStore creates an empty instance store.
func NewStore() *Store {
	return &Store{
		types: make(map[TypeName][]Validator),
		obj:   make(map[ObjectID]*objState),
		idem:  make(map[string]idemRecord),
	}
}

// Clock returns the store-wide logical tick count, advanced exactly once per
// accepted write and never on a rejected path.
func (s *Store) Clock() uint64 {
	return s.clock.Load()
}

func (s *Store) stateFor(id ObjectID) *objState {
	s.objMu.Lock()
	defer s.objMu.Unlock()
	st := s.obj[id]
	if st == nil {
		st = &objState{
			localMark: make(map[Property]Version),
			versions:  []*snapshot{nil},
		}
		s.obj[id] = st
	}
	return st
}

func (s *Store) stateForLocked(id ObjectID) *objState {
	st := s.obj[id]
	if st == nil {
		st = &objState{
			localMark: make(map[Property]Version),
			versions:  []*snapshot{nil},
		}
		s.obj[id] = st
	}
	return st
}

func (st *objState) snapshotAt(v Version) *snapshot {
	if v <= 0 || int(v) >= len(st.versions) {
		return nil
	}
	return st.versions[v]
}
