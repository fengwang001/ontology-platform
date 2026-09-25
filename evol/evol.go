// Package evol implements versioned record writes/reads with writer-time
// frozen defaults. It depends only on sch.
package evol

import (
	"errors"
	"sync"

	"ontology/sch"
)

// Sentinel errors: the three failure modes are deliberately distinct.
var (
	ErrInvalidWriteVersion = errors.New("evol: write version out of range")
	ErrUnknownField        = errors.New("evol: value for field absent from writer schema")
	ErrInvalidReadVersion  = errors.New("evol: read version out of range")
)

// Record is an immutable written record: writer version W plus explicit values.
type Record struct {
	w      int
	values map[string]int // copy owned solely by the Record, never mutated
}

// W returns the writer version.
func (r Record) W() int { return r.w }

// Explicit reports the record's own stored value for field, without applying
// any default. It lets external reference implementations inspect explicit
// values independently of Read.
func Explicit(r Record, field string) (int, bool) {
	v, ok := r.values[field]
	return v, ok
}

// Store keeps records in process memory. The zero value is not usable; use New.
type Store struct {
	t   *sch.Table
	mu  sync.RWMutex
	rec []Record
}

// New creates a Store over the given schema table.
func New(t *sch.Table) *Store { return &Store{t: t} }

// Len reports how many records have been successfully written.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rec)
}

// Write validates everything first; only after every check passes does it copy
// the values and append, so a rejected call leaves no state behind.
func (s *Store) Write(w int, values map[string]int) (Record, error) {
	if !s.t.ValidVersion(w) {
		return Record{}, ErrInvalidWriteVersion
	}
	for f := range values {
		if f == "" || !s.t.Has(w, f) {
			return Record{}, ErrUnknownField
		}
	}
	cp := make(map[string]int, len(values))
	for f, v := range values {
		cp[f] = v
	}
	rec := Record{w: w, values: cp}
	s.mu.Lock()
	s.rec = append(s.rec, rec)
	s.mu.Unlock()
	return rec, nil
}

// Read resolves rec under reader version R:
//   - fields of schema[R] present in rec keep their explicit value;
//   - missing fields fall back to def(f, W), frozen at the writer version;
//   - fields unknown to schema[R] are silently ignored.
func (s *Store) Read(rec Record, r int) (map[string]int, error) {
	if !s.t.ValidVersion(r) {
		return nil, ErrInvalidReadVersion
	}
	out := make(map[string]int)
	for _, f := range s.t.Fields(r) {
		if v, ok := rec.values[f]; ok {
			out[f] = v
			continue
		}
		d, ok := s.t.Def(f, rec.w) // only missing fields consult the frozen default
		if !ok {
			return nil, ErrUnknownField
		}
		out[f] = d
	}
	return out, nil
}
