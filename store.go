package ontology

import (
	"fmt"
	"sync"
)

// Store is an in-memory record store with unique-constraint checking.
// Stored values are kept byte-identical to what callers inserted;
// normalization only affects the comparison index. All methods are
// safe for concurrent use.
type Store struct {
	mu          sync.Mutex
	norm        Normalize
	constraints []Constraint
	records     map[string]map[string]Value // pk -> raw properties
	index       []map[string]string         // per constraint: normalized key -> pk
}

// NewStore creates a Store with the given normalization toggles and
// unique constraints.
func NewStore(norm Normalize, constraints ...Constraint) *Store {
	s := &Store{
		norm:        norm,
		constraints: constraints,
		records:     make(map[string]map[string]Value),
		index:       make([]map[string]string, len(constraints)),
	}
	for i := range s.index {
		s.index[i] = make(map[string]string)
	}
	return s
}

// Insert adds a record. It returns a *ConflictError if any unique
// constraint is violated, or an error if the pk already exists.
func (s *Store) Insert(pk string, props map[string]Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.records[pk]; dup {
		return fmt.Errorf("record %q already exists", pk)
	}
	if err := s.checkLocked(pk, props, s.index, s.records); err != nil {
		return err
	}
	s.putLocked(pk, props, s.index, s.records)
	return nil
}

// Delete removes a record and its index entries. Missing pks are no-ops.
func (s *Store) Delete(pk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleteLocked(pk, s.constraints, s.norm, s.index, s.records)
}

// Get returns a copy of the stored raw values, byte-identical to what
// was inserted (original case, whitespace and code points preserved).
func (s *Store) Get(pk string) (map[string]Value, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	props, ok := s.records[pk]
	if !ok {
		return nil, false
	}
	out := make(map[string]Value, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out, true
}

// Len reports the number of live records.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// checkLocked returns a *ConflictError if props violate any constraint
// against the given index/records, nil otherwise.
func (s *Store) checkLocked(pk string, props map[string]Value,
	index []map[string]string, records map[string]map[string]Value) *ConflictError {
	for i, c := range s.constraints {
		key, ok := c.key(s.norm, props)
		if !ok {
			continue
		}
		if holder, taken := index[i][key]; taken && holder != pk {
			return &ConflictError{
				Constraint: c.Name,
				Key:        key,
				ExistingPK: holder,
				Incoming:   pickCols(c, props),
				Existing:   pickCols(c, records[holder]),
			}
		}
	}
	return nil
}

// putLocked stores a defensive copy of props and indexes it.
func (s *Store) putLocked(pk string, props map[string]Value,
	index []map[string]string, records map[string]map[string]Value) {
	cp := make(map[string]Value, len(props))
	for k, v := range props {
		cp[k] = v
	}
	records[pk] = cp
	for i, c := range s.constraints {
		if key, ok := c.key(s.norm, cp); ok {
			index[i][key] = pk
		}
	}
}

// deleteLocked removes pk and its index entries if present.
func deleteLocked(pk string, constraints []Constraint, norm Normalize,
	index []map[string]string, records map[string]map[string]Value) {
	props, ok := records[pk]
	if !ok {
		return
	}
	for i, c := range constraints {
		if key, ok := c.key(norm, props); ok {
			delete(index[i], key)
		}
	}
	delete(records, pk)
}
