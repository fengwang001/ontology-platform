package ontology

import "sync"

// Instance is the internal, raw representation of one object instance.
// Present is the authoritative record of which properties hold values; raw
// values of absent properties are never consulted.
type Instance struct {
	Type    string
	ID      string
	Values  map[string]Value
	Present map[string]bool
}

// Store is the concurrency-safe instance store.
type Store struct {
	mu        sync.Mutex
	instances map[string]*record
	clock     func() int64
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{instances: map[string]*record{}, clock: zeroClock}
}

func zeroClock() int64 { return 0 }

// SetClock installs a monotonic nanosecond clock used for last-write times.
func (s *Store) SetClock(clock func() int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
}

func recordKey(typeName, id string) string { return typeName + "\x00" + id }

type record struct {
	instance      Instance
	version       int64
	lastWriteNano int64
}

// Put creates or replaces an instance.
func (s *Store) Put(inst Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	present := inst.Present
	if present == nil {
		present = map[string]bool{}
		for name := range inst.Values {
			present[name] = true
		}
	}
	values := map[string]Value{}
	for name, v := range inst.Values {
		values[name] = v
	}
	rec := &record{
		instance: Instance{
			Type:    inst.Type,
			ID:      inst.ID,
			Values:  values,
			Present: present,
		},
		version: 1,
	}
	s.instances[recordKey(inst.Type, inst.ID)] = rec
}

// GetRaw returns a deep copy of the raw instance and whether it exists.
func (s *Store) GetRaw(typeName, id string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.instances[recordKey(typeName, id)]
	if !ok {
		return Instance{}, false
	}
	return rec.instance.clone(), true
}

// applyWrite atomically applies accepted field writes, bumping version and
// timestamp only when at least one field is accepted.
//
// It is called only after the write has been fully adjudicated: values are
// the final values to store (mask already applied when required), so a
// rejected call never reaches this function and can never mutate state,
// version or timestamp.
func (s *Store) applyWrite(typeName, id string, values map[string]Value) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.instances[recordKey(typeName, id)]
	if !ok {
		return 0, false
	}
	if len(values) == 0 {
		return rec.version, false
	}
	for name, v := range values {
		rec.instance.Values[name] = v
		rec.instance.Present[name] = true
	}
	rec.version++
	rec.lastWriteNano = s.clock()
	return rec.version, true
}

// Version reports the current version of an instance (0 if unknown).
func (s *Store) Version(typeName, id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.instances[recordKey(typeName, id)]; ok {
		return rec.version
	}
	return 0
}

// LastWriteNanos reports the last successful-write timestamp (0 if none).
func (s *Store) LastWriteNanos(typeName, id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.instances[recordKey(typeName, id)]; ok {
		return rec.lastWriteNano
	}
	return 0
}

func (i Instance) clone() Instance {
	values := make(map[string]Value, len(i.Values))
	for k, v := range i.Values {
		values[k] = v
	}
	present := make(map[string]bool, len(i.Present))
	for k, v := range i.Present {
		present[k] = v
	}
	return Instance{Type: i.Type, ID: i.ID, Values: values, Present: present}
}

// InstanceCount returns how many instances are stored. Used by overhead
// observability tests to show adjudication cost is independent of it.
func (s *Store) InstanceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.instances)
}
