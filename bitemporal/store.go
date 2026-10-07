package bitemporal

import (
	"errors"
	"fmt"
	"sync"
)

// Store is the append-only bi-temporal record log.
//
// Concurrency contract: the lock linearizes every Write and every snapshot
// read. Freezing an export captures (clock, maxSeq) in one critical section,
// so the binding is a single point in the global serial order; all subsequent
// batches read the same immutable prefix of the record log.
type Store struct {
	mu           sync.RWMutex
	retainedFrom Tick
	clock        Tick
	seq          uint64
	log          []*Record
	byObject     map[string][]*Record
	indexes      map[string]*segmentIndex
	schemas      map[string]*Schema
	tampered     map[uint64]bool // test-only forced corruption flags
}

// NewStore creates a store whose retained horizon starts at retainedFrom.
func NewStore(retainedFrom, initialClock Tick) *Store {
	if initialClock < retainedFrom {
		initialClock = retainedFrom
	}
	return &Store{
		retainedFrom: retainedFrom,
		clock:        initialClock,
		byObject:     map[string][]*Record{},
		indexes:      map[string]*segmentIndex{},
		schemas:      map[string]*Schema{},
		tampered:     map[uint64]bool{},
	}
}

// RetainedFrom returns the earliest queryable transaction time.
func (s *Store) RetainedFrom() Tick { return s.retainedFrom }

// RegisterSchema defines an object type at definedAt. Redefinition is rejected.
func (s *Store) RegisterSchema(name string, definedAt Tick, fields map[string]FieldKind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return errors.New("schema name must not be empty")
	}
	if definedAt < s.retainedFrom {
		return &ExportError{Code: CodeRetention, Msg: "schema definition predates retained horizon"}
	}
	if _, exists := s.schemas[name]; exists {
		return fmt.Errorf("schema %q already registered", name)
	}
	cp := make(map[string]FieldKind, len(fields))
	for k, kd := range fields {
		if !validFieldKind(kd) {
			return &ExportError{Code: CodeInvalidRange, Msg: "unknown field kind " + string(kd)}
		}
		cp[k] = kd
	}
	s.schemas[name] = &Schema{Name: name, DefinedAt: definedAt, Fields: cp}
	return nil
}

// Write appends one record at the current clock time, assigning arrival seq.
// The record's TxTime equals the clock value at the linearization point, so
// writes in the same tick tie-break by arrival seq.
func (s *Store) Write(objectID, typeName string, iv Interval, value Value) (*Record, error) {
	if objectID == "" {
		return nil, errors.New("object id must not be empty")
	}
	if iv.Start >= iv.End {
		return nil, &ExportError{Code: CodeInvalidRange, Msg: "record interval must be half-open with start < end"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	schema, ok := s.schemas[typeName]
	if !ok {
		return nil, &ExportError{Code: CodeSchemaUndefined, Msg: "type not registered: " + typeName}
	}
	if err := validateValue(schema, value); err != nil {
		return nil, err
	}
	s.seq++
	rec := &Record{
		ObjectID: objectID,
		TypeName: typeName,
		Start:    iv.Start,
		End:      iv.End,
		TxTime:   s.clock,
		Seq:      s.seq,
		Value:    value,
	}
	rec.Hash = rec.computeHash()
	s.log = append(s.log, rec)
	s.byObject[objectID] = append(s.byObject[objectID], rec)
	idx := s.indexes[objectID]
	if idx == nil {
		idx = newSegmentIndex()
		s.indexes[objectID] = idx
	}
	idx.add(rec)
	return cloneRecord(rec), nil
}

// AdvanceClock moves the system transaction clock forward to t (never back).
func (s *Store) AdvanceClock(t Tick) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t > s.clock {
		s.clock = t
	}
}

// Clock returns the current transaction clock.
func (s *Store) Clock() Tick {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clock
}

// freeze returns the atomic (clock, arrival-seq) binding. Both values are read
// in one critical section, defining the exact linearization point.
func (s *Store) freeze() Cutoff {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Cutoff{T: s.clock, Seq: s.seq}
}

// schemaAt returns the schema if it was defined no later than t.
func (s *Store) schemaAt(typeName string, t Tick) (*Schema, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.schemas[typeName]
	if !ok || sc.DefinedAt > t {
		return nil, false
	}
	return sc, true
}

// objectIndex returns the live segment index for one object (nil if absent).
func (s *Store) objectIndex(objectID string) *segmentIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexes[objectID]
}

// recordBySeq resolves an arrival seq to its record for value retrieval.
func (s *Store) recordBySeq(seq uint64) *Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq == 0 || seq > uint64(len(s.log)) {
		return nil
	}
	return s.log[seq-1]
}

// objectRecords returns the immutable records of objectID with Seq <= c.Seq,
// verifying each record's integrity hash. Used by exports and audits.
func (s *Store) objectRecords(objectID string, c Cutoff) ([]*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byObject[objectID]
	out := make([]*Record, 0, len(src))
	for _, r := range src {
		if r.Seq > c.Seq {
			continue
		}
		if s.tampered[r.Seq] {
			return nil, &ExportError{Code: CodeInconsistency, Msg: fmt.Sprintf("record seq=%d flagged inconsistent", r.Seq)}
		}
		if r.Hash != r.computeHash() {
			return nil, &ExportError{Code: CodeInconsistency, Msg: fmt.Sprintf("record seq=%d integrity hash mismatch", r.Seq)}
		}
		out = append(out, r)
	}
	return out, nil
}

// allObjects lists every object id (order is defined by first-seen seq).
func (s *Store) allObjects(c Cutoff) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool, len(s.byObject))
	out := make([]string, 0, len(s.byObject))
	for _, r := range s.log {
		if r.Seq > c.Seq {
			break
		}
		if s.tampered[r.Seq] || r.Hash != r.computeHash() {
			return nil, &ExportError{Code: CodeInconsistency, Msg: fmt.Sprintf("record seq=%d inconsistent", r.Seq)}
		}
		if !seen[r.ObjectID] {
			seen[r.ObjectID] = true
			out = append(out, r.ObjectID)
		}
	}
	return out, nil
}

// markTampered forces an integrity failure for one seq (test support only).
func (s *Store) markTampered(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tampered[seq] = true
}

func validFieldKind(k FieldKind) bool {
	switch k {
	case KindString, KindInt, KindFloat, KindBool:
		return true
	}
	return false
}

func validateValue(schema *Schema, v Value) error {
	if len(v.Fields) != len(schema.Fields) {
		return &ExportError{Code: CodeInvalidRange, Msg: "value field count does not match schema"}
	}
	for name, kind := range schema.Fields {
		raw, ok := v.Fields[name]
		if !ok {
			return &ExportError{Code: CodeInvalidRange, Msg: "missing field " + name}
		}
		switch kind {
		case KindString:
			if _, ok := raw.(string); !ok {
				return &ExportError{Code: CodeInvalidRange, Msg: "field " + name + " must be string"}
			}
		case KindInt:
			if _, ok := raw.(int64); !ok {
				return &ExportError{Code: CodeInvalidRange, Msg: "field " + name + " must be int64"}
			}
		case KindFloat:
			if _, ok := raw.(float64); !ok {
				return &ExportError{Code: CodeInvalidRange, Msg: "field " + name + " must be float64"}
			}
		case KindBool:
			if _, ok := raw.(bool); !ok {
				return &ExportError{Code: CodeInvalidRange, Msg: "field " + name + " must be bool"}
			}
		}
	}
	return nil
}

func cloneRecord(r *Record) *Record {
	cp := *r
	if len(r.Value.Fields) > 0 {
		cp.Value.Fields = make(map[string]any, len(r.Value.Fields))
		for k, v := range r.Value.Fields {
			cp.Value.Fields[k] = v
		}
	}
	return &cp
}
