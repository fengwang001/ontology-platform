package ontology

import (
	"errors"
	"sync"
)

// instance is the live, individually lockable state of one object.
type instance struct {
	mu      sync.Mutex
	id      ID
	typ     string
	version uint64
	step    uint64
	attrs   map[Attr]Value
	out     map[string]map[ID]struct{}
	in      map[string]map[ID]struct{}
}

// Store is the concurrency-safe in-memory ontology state.
//
// Concurrency control is strict two-phase locking with a global lock
// ordering on instance IDs. A batch acquires the locks of every instance it
// touches in ascending ID order, performs the three staged gates and the
// whole mutation while holding them, then releases them together. Batches
// whose instance sets intersect are therefore serialised: exactly one of two
// batches competing for the same precondition version can observe that
// version and commit; the other reads the bumped version and is rejected.
type Store struct {
	instanceMu  sync.Mutex
	instances   map[ID]*instance
	objectTypes map[string]ObjectType
	linkTypes   map[string]LinkType

	orderMu sync.Mutex
	order   uint64

	journal *Journal

	// acquireCounter is incremented at the lock-acquisition point and is
	// exposed through AcquireOrder for tests that need to reconstruct the
	// exact linearisation (lock-acquisition) order. It is part of the
	// no-extra-state cost story: a single counter touched O(k) per batch.
	acquireMu      sync.Mutex
	acquireCounter uint64

	// testHookAcquired, when set, is invoked after a batch acquired its
	// instance locks and before the version gate runs. Tests use it to park
	// a batch at the linearisation point; production code leaves it nil.
	testHookAcquired func(ids []ID)
}

// Config parameterises a new store.
type Config struct {
	ObjectTypes map[string]ObjectType
	LinkTypes   map[string]LinkType
}

// New creates an empty store.
func New(cfg Config) *Store {
	objectTypes := map[string]ObjectType{}
	for k, v := range cfg.ObjectTypes {
		objectTypes[k] = v
	}
	linkTypes := map[string]LinkType{}
	for k, v := range cfg.LinkTypes {
		linkTypes[k] = v
	}
	return &Store{
		instances:   map[ID]*instance{},
		objectTypes: objectTypes,
		linkTypes:   linkTypes,
		journal:     NewJournal(),
	}
}

// ErrExists is returned when creating an instance whose ID is taken.
var ErrExists = errors.New("instance already exists")

// ErrUnknownType is returned when a referenced object type is not registered.
var ErrUnknownType = errors.New("unknown object type")

// CreateInstance inserts a new instance at version 1 with the default
// per-instance promotion step of 1.
func (s *Store) CreateInstance(id ID, typ string) error {
	if _, ok := s.objectTypes[typ]; !ok {
		return ErrUnknownType
	}
	s.instanceMu.Lock()
	defer s.instanceMu.Unlock()
	if _, ok := s.instances[id]; ok {
		return ErrExists
	}
	s.instances[id] = &instance{
		id:      id,
		typ:     typ,
		version: 1,
		step:    1,
		attrs:   map[Attr]Value{},
		out:     map[string]map[ID]struct{}{},
		in:      map[string]map[ID]struct{}{},
	}
	s.journal.appendLocked(Record{Kind: "create", ID: id, Type: typ})
	return nil
}

// Journal returns the append-only decision journal.
func (s *Store) Journal() *Journal { return s.journal }

// Get returns the current snapshot of one instance.
func (s *Store) Get(id ID) (Snapshot, bool) {
	s.instanceMu.Lock()
	it, ok := s.instances[id]
	s.instanceMu.Unlock()
	if !ok {
		return Snapshot{}, false
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return snapshotOf(it), true
}

// SnapshotAll returns snapshots of the given instances in O(k) map lookups;
// the cost is independent of the total number of instances in the store.
func (s *Store) SnapshotAll(ids []ID) map[ID]Snapshot {
	out := make(map[ID]Snapshot, len(ids))
	for _, id := range ids {
		if snap, ok := s.Get(id); ok {
			out[id] = snap
		}
	}
	return out
}

func snapshotOf(it *instance) Snapshot {
	attrs := make(map[Attr]Value, len(it.attrs))
	for k, v := range it.attrs {
		attrs[k] = v
	}
	return Snapshot{ID: it.id, Type: it.typ, Version: it.version, Step: it.step, Attrs: attrs}
}
