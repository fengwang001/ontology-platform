package ontology

import (
	"fmt"
	"strings"
	"sync"
)

// Record is a stored object: a primary key plus its properties.
// Props always holds the original values exactly as supplied by
// the caller; normalization only affects the internal index.
type Record struct {
	ID    string
	Props map[string]Value
}

// Store keeps records and enforces unique constraints with
// normalized keys. It is safe for concurrent use.
type Store struct {
	mu          sync.Mutex
	norm        NormOptions
	constraints []Constraint
	records     map[string]Record
	// index maps constraint name -> encoded normalized key ->
	// primary key of the live record holding that key.
	index map[string]map[string]string
}

// New creates a Store with the given normalization options and
// unique constraints.
func New(norm NormOptions, constraints ...Constraint) *Store {
	s := &Store{
		norm:        norm,
		constraints: constraints,
		records:     map[string]Record{},
		index:       map[string]map[string]string{},
	}
	for _, c := range constraints {
		s.index[c.Name] = map[string]string{}
	}
	return s
}

// Get returns a copy of the record with the given primary key.
// Property values are byte-identical to what was inserted.
func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return Record{}, false
	}
	return Record{ID: rec.ID, Props: cloneProps(rec.Props)}, true
}

// Insert adds a record. It returns a *ConflictError when a unique
// constraint is violated, leaving the store unchanged.
func (s *Store) Insert(id string, props map[string]Value) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[id]; exists {
		return fmt.Errorf("record %q already exists", id)
	}
	if err := s.checkAll(id, props, s.records, s.index); err != nil {
		return err
	}
	s.applyInsert(s.records, s.index, id, props)
	return nil
}

// Delete removes the record with the given primary key, if any.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyDelete(s.records, s.index, id)
}

// checkAll validates props against every constraint. records and
// index are the state to check against (live or staged).
func (s *Store) checkAll(id string, props map[string]Value, records map[string]Record, index map[string]map[string]string) *ConflictError {
	for _, c := range s.constraints {
		vals, ok := c.normValues(props, s.norm)
		if !ok {
			continue
		}
		key := encodeKey(vals)
		if existingID, hit := index[c.Name][key]; hit && existingID != id {
			existing := records[existingID]
			return &ConflictError{
				Constraint: c.Name,
				ExistingID: existingID,
				Key:        strings.Join(vals, " | "),
				Incoming:   columnValues(c, props),
				Existing:   columnValues(c, existing.Props),
			}
		}
	}
	return nil
}

// applyInsert mutates records and index without checking.
func (s *Store) applyInsert(records map[string]Record, index map[string]map[string]string, id string, props map[string]Value) {
	records[id] = Record{ID: id, Props: cloneProps(props)}
	for _, c := range s.constraints {
		if vals, ok := c.normValues(props, s.norm); ok {
			index[c.Name][encodeKey(vals)] = id
		}
	}
}

// applyDelete mutates records and index without checking.
func (s *Store) applyDelete(records map[string]Record, index map[string]map[string]string, id string) {
	rec, ok := records[id]
	if !ok {
		return
	}
	for _, c := range s.constraints {
		if vals, ok := c.normValues(rec.Props, s.norm); ok {
			delete(index[c.Name], encodeKey(vals))
		}
	}
	delete(records, id)
}

func columnValues(c Constraint, props map[string]Value) []Value {
	vals := make([]Value, len(c.Columns))
	for i, col := range c.Columns {
		vals[i] = props[col] // missing property reads as NULL
	}
	return vals
}

func cloneProps(props map[string]Value) map[string]Value {
	out := make(map[string]Value, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}
