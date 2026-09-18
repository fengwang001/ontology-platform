package instance

import (
	"sync"
	"time"
)

// Store is an in-memory, concurrency-safe store of object instances.
// State lives only for the lifetime of the process.
type Store struct {
	mu      sync.Mutex
	records map[recordKey]*record
	now     func() time.Time
}

type recordKey struct {
	objectType string
	key        string
}

type record struct {
	instance Instance
}

// NewStore creates an empty store using the wall clock for write times.
func NewStore() *Store {
	return &Store{
		records: make(map[recordKey]*record),
		now:     time.Now,
	}
}

// newStoreWithClock is used by tests that need deterministic write times.
func newStoreWithClock(now func() time.Time) *Store {
	s := NewStore()
	s.now = now
	return s
}

// Create inserts objectType/key with the given attributes. If the primary key
// existed and was logically deleted, it is resurrected and its version
// continues from before the delete (never reset to 1).
func (s *Store) Create(objectType, key string, attributes map[string]any) (*Instance, error) {
	attrs, err := cloneAttributes(attributes)
	if err != nil {
		return nil, &WriteError{
			Reason:     ReasonConstraint,
			ObjectType: objectType,
			Key:        key,
			Err:        err,
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rk := recordKey{objectType, key}
	rec := s.records[rk]
	if rec != nil && !rec.instance.Deleted {
		return nil, &WriteError{
			Reason:     ReasonAlreadyExists,
			ObjectType: objectType,
			Key:        key,
			Actual:     rec.instance.Version,
		}
	}

	now := s.now()
	if rec == nil {
		rec = &record{}
		s.records[rk] = rec
		rec.instance.Version = 1
	} else {
		rec.instance.Version++
	}
	rec.instance.ObjectType = objectType
	rec.instance.Key = key
	rec.instance.Attributes = attrs
	rec.instance.LastWriteTime = now
	rec.instance.Deleted = false

	return snapshotInstance(&rec.instance), nil
}

// Get returns an independent snapshot of a live instance. A logically deleted
// instance is reported with ReasonDeleted (carrying its last version) and a
// primary key that never existed with ReasonNotFound.
func (s *Store) Get(objectType, key string) (*Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.records[recordKey{objectType, key}]
	if rec == nil {
		return nil, &WriteError{Reason: ReasonNotFound, ObjectType: objectType, Key: key}
	}
	if rec.instance.Deleted {
		return nil, &WriteError{
			Reason:     ReasonDeleted,
			ObjectType: objectType,
			Key:        key,
			Actual:     rec.instance.Version,
		}
	}
	return snapshotInstance(&rec.instance), nil
}
