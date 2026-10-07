package ontology

import "sync"

// store is the raw instance storage backend.
//
// Concurrency: one RWMutex per instance id gives linearizable per-key
// read/write semantics. Readers take RLock (multiple concurrent reads
// of the same instance never block each other), writers take Lock.
// Because adjudication reads raw state only under that key's lock and
// computes the derived view from a point-in-time clone, every batch of
// concurrent calls is equivalent to some serial interleaving, and a
// rejected write mutates nothing and hence affects no later call.
//
// The key mutex map itself is guarded by its own lock; mutexes are
// never removed so a held lock can never vanish beneath a goroutine.
type store struct {
	tmu sync.Mutex

	mu    sync.RWMutex
	data  map[string]*Instance
	locks map[string]*sync.RWMutex
}

func newStore() *store {
	return &store{data: map[string]*Instance{}, locks: map[string]*sync.RWMutex{}}
}

func (s *store) keyLock(id string) *sync.RWMutex {
	s.mu.Lock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.RWMutex{}
		s.locks[id] = l
	}
	s.mu.Unlock()
	return l
}

// put installs a freshly constructed instance. Used at registration;
// writes go through mutate.
func (s *store) put(inst *Instance) {
	l := s.keyLock(inst.ID)
	l.Lock()
	defer l.Unlock()
	s.mu.Lock()
	s.data[inst.ID] = inst
	s.mu.Unlock()
}

// readView returns a point-in-time clone under the key's read lock.
func (s *store) readView(id string) (*Instance, bool) {
	l := s.keyLock(id)
	l.RLock()
	defer l.RUnlock()
	s.mu.RLock()
	inst, ok := s.data[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return inst.clone(), true
}

// mutate runs fn against the live instance under the key's write lock.
// fn returns the new cells to commit, or an error to abort. An abort
// leaves value, Version and LastWriteID completely untouched. The
// commit (including version bump) happens atomically at the end.
func (s *store) mutate(id string, fn func(inst *Instance) (map[string]rawCell, *DecisionError)) (*Instance, *DecisionError) {
	l := s.keyLock(id)
	l.Lock()
	defer l.Unlock()
	s.mu.RLock()
	inst, ok := s.data[id]
	s.mu.RUnlock()
	if !ok {
		return nil, &DecisionError{ErrInstanceNotFound, "instance not found: " + id}
	}
	newCells, err := fn(inst)
	if err != nil {
		return nil, err
	}
	inst.cells = newCells
	inst.Version++
	inst.LastWriteID++
	return inst.clone(), nil
}
