package instance

import (
	"fmt"
	"sync"
	"time"
)

// record is the internal storage state. Deleted records are kept as
// tombstones so that a later Create resumes the version sequence.
type record struct {
	props     map[string]any
	version   int64
	lastWrite time.Time
	deleted   bool
}

type typeKey struct {
	objectType string
	key        PrimaryKey
}

// Store is an in-memory, concurrency-safe store of object instances
// with optimistic concurrency control and logical deletion.
type Store struct {
	mu    sync.RWMutex
	types map[string]TypeSpec
	data  map[typeKey]*record

	// now is indirected so tests can control write timestamps.
	now func() time.Time
}

// NewStore creates an empty store. specs may register property
// constraints for object types.
func NewStore(specs ...TypeSpec) *Store {
	s := &Store{
		types: make(map[string]TypeSpec),
		data:  make(map[typeKey]*record),
		now:   time.Now,
	}
	for _, spec := range specs {
		s.types[spec.Name] = spec
	}
	return s
}

// Create inserts a new instance. Version becomes 1 for a never-seen
// key. If the key was logically deleted, the instance is resurrected
// and its version continues one past the version it had at deletion
// time; it never restarts at 1.
func (s *Store) Create(objectType string, key PrimaryKey, properties map[string]any) (*Instance, error) {
	if err := s.validate(objectType, key, properties); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tk := typeKey{objectType, key}
	if r, ok := s.data[tk]; ok {
		if r.deleted {
			r.deleted = false
			r.version++
			r.props = deepCopyProperties(properties)
			r.lastWrite = s.now()
			return s.snapshotLocked(objectType, key, r), nil
		}
		return nil, kindError(KindExists, objectType, key)
	}

	r := &record{
		props:     deepCopyProperties(properties),
		version:   1,
		lastWrite: s.now(),
	}
	s.data[tk] = r
	return s.snapshotLocked(objectType, key, r), nil
}

// Get returns an independent deep copy of a live instance. A
// never-existing key yields KindNotFound; a logically deleted key
// yields KindDeleted.
func (s *Store) Get(objectType string, key PrimaryKey) (*Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.data[typeKey{objectType, key}]
	if !ok {
		return nil, kindError(KindNotFound, objectType, key)
	}
	if r.deleted {
		return nil, kindError(KindDeleted, objectType, key)
	}
	return s.snapshotLocked(objectType, key, r), nil
}

// Update replaces the properties of an existing instance and advances
// its version by one. expectedVersion must equal the version returned
// by the caller's last read; otherwise a KindConflict error carrying
// the actual version is returned. Updating a deleted instance returns
// KindDeleted, not a conflict; updating a never-existing key returns
// KindNotFound.
func (s *Store) Update(objectType string, key PrimaryKey, expectedVersion int64, properties map[string]any) (*Instance, error) {
	if err := s.validate(objectType, key, properties); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.data[typeKey{objectType, key}]
	if !ok {
		return nil, kindError(KindNotFound, objectType, key)
	}
	if r.deleted {
		return nil, kindError(KindDeleted, objectType, key)
	}
	if r.version != expectedVersion {
		return nil, &Error{
			Kind:       KindConflict,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     r.version,
		}
	}

	r.props = deepCopyProperties(properties)
	r.version++
	r.lastWrite = s.now()
	return s.snapshotLocked(objectType, key, r), nil
}

// Delete logically removes an instance with the same version rules as
// Update. The record is retained as a tombstone with its current
// version and last write time unchanged.
func (s *Store) Delete(objectType string, key PrimaryKey, expectedVersion int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.data[typeKey{objectType, key}]
	if !ok {
		return kindError(KindNotFound, objectType, key)
	}
	if r.deleted {
		return kindError(KindDeleted, objectType, key)
	}
	if r.version != expectedVersion {
		return &Error{
			Kind:       KindConflict,
			ObjectType: objectType,
			Key:        key,
			Expected:   expectedVersion,
			Actual:     r.version,
		}
	}

	r.deleted = true
	return nil
}

// OpKind selects the operation carried by one BatchWrite entry.
type OpKind int

const (
	OpCreate OpKind = iota + 1
	OpUpdate
	OpDelete
)

// Op is one entry in a BatchWrite. ExpectedVersion is required for
// OpUpdate and OpDelete and ignored for OpCreate. Properties is used
// by OpCreate and OpUpdate.
type Op struct {
	Kind            OpKind
	ObjectType      string
	Key             PrimaryKey
	ExpectedVersion int64
	Properties      map[string]any
}

// BatchWrite applies a mixed set of create/update/delete operations
// atomically. If any operation fails a version check, the same key
// occurs more than once in the batch, or properties violate a
// constraint, nothing is applied: versions do not advance and last
// write times do not change. The returned *BatchError identifies the
// offending index, key and reason.
func (s *Store) BatchWrite(ops []Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[typeKey]int, len(ops))
	for i, op := range ops {
		tk := typeKey{op.ObjectType, op.Key}
		if first, dup := seen[tk]; dup {
			return &BatchError{
				Index:  i,
				Key:    op.Key,
				Kind:   KindDuplicateKey,
				Reason: fmt.Sprintf("key also used by op %d", first),
			}
		}
		seen[tk] = i
	}

	// Validate and version-check everything against the committed
	// state before mutating anything. The apply phase that follows
	// cannot fail, so rejecting here guarantees the batch is atomic:
	// no version advances and no timestamp changes on failure.
	for i, op := range ops {
		if be := s.checkOpLocked(i, op); be != nil {
			return be
		}
	}

	now := s.now()
	for _, op := range ops {
		tk := typeKey{op.ObjectType, op.Key}
		switch op.Kind {
		case OpCreate:
			r := s.data[tk]
			if r == nil {
				// version 0 here so the uniform increment below lands
				// on 1 for a never-seen key.
				r = &record{}
				s.data[tk] = r
			}
			r.deleted = false
			r.version++
			r.props = deepCopyProperties(op.Properties)
			r.lastWrite = now
		case OpUpdate:
			r := s.data[tk]
			r.props = deepCopyProperties(op.Properties)
			r.version++
			r.lastWrite = now
		case OpDelete:
			r := s.data[tk]
			r.deleted = true
		}
	}
	return nil
}

// checkOpLocked validates one operation against the committed state
// without mutating it.
func (s *Store) checkOpLocked(i int, op Op) *BatchError {
	tk := typeKey{op.ObjectType, op.Key}

	switch op.Kind {
	case OpCreate, OpUpdate:
		if be := s.checkConstraintLocked(i, op); be != nil {
			return be
		}
	}

	switch op.Kind {
	case OpCreate:
		if r, ok := s.data[tk]; ok && !r.deleted {
			return &BatchError{Index: i, Key: op.Key, Kind: KindExists}
		}
	case OpUpdate:
		r, ok := s.data[tk]
		if !ok {
			return &BatchError{Index: i, Key: op.Key, Kind: KindNotFound}
		}
		if r.deleted {
			return &BatchError{Index: i, Key: op.Key, Kind: KindDeleted}
		}
		if r.version != op.ExpectedVersion {
			return &BatchError{
				Index:    i,
				Key:      op.Key,
				Kind:     KindConflict,
				Expected: op.ExpectedVersion,
				Actual:   r.version,
			}
		}
	case OpDelete:
		r, ok := s.data[tk]
		if !ok {
			return &BatchError{Index: i, Key: op.Key, Kind: KindNotFound}
		}
		if r.deleted {
			return &BatchError{Index: i, Key: op.Key, Kind: KindDeleted}
		}
		if r.version != op.ExpectedVersion {
			return &BatchError{
				Index:    i,
				Key:      op.Key,
				Kind:     KindConflict,
				Expected: op.ExpectedVersion,
				Actual:   r.version,
			}
		}
	default:
		return &BatchError{Index: i, Key: op.Key, Kind: KindConstraint, Reason: "unknown op kind"}
	}
	return nil
}

func (s *Store) checkConstraintLocked(i int, op Op) *BatchError {
	if err := s.validate(op.ObjectType, op.Key, op.Properties); err != nil {
		be := &BatchError{Index: i, Key: op.Key}
		if e, ok := err.(*Error); ok {
			be.Kind = e.Kind
			be.Reason = e.Reason
		} else {
			be.Kind = KindConstraint
			be.Reason = err.Error()
		}
		return be
	}
	return nil
}

func (s *Store) snapshotLocked(objectType string, key PrimaryKey, r *record) *Instance {
	return &Instance{
		ObjectType:    objectType,
		Key:           key,
		Properties:    deepCopyProperties(r.props),
		Version:       r.version,
		LastWriteTime: r.lastWrite,
	}
}
