package cascade

import "sync"

// MemoryStore is an in-memory graph implementation safe for concurrent use.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string]struct{}
	links   map[string]Link
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: map[string]struct{}{}, links: map[string]Link{}}
}

func (s *MemoryStore) AddObject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id] = struct{}{}
}

func (s *MemoryStore) AddLink(l Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[l.Src] = struct{}{}
	s.objects[l.Dst] = struct{}{}
	s.links[l.ID] = l
	return nil
}

func (s *MemoryStore) HasObject(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[id]
	return ok
}

func (s *MemoryStore) Links() []Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Link, 0, len(s.links))
	for _, l := range s.links {
		out = append(out, l)
	}
	return out
}

func (s *MemoryStore) DeleteObjects(ids map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range ids {
		delete(s.objects, id)
	}
}

func (s *MemoryStore) DeleteLinks(ids map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range ids {
		delete(s.links, id)
	}
}

// snapshot copies the graph under the store lock; callers must not hold e.mu.
func (s *MemoryStore) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	objs := make(map[string]bool, len(s.objects))
	for id := range s.objects {
		objs[id] = true
	}
	ls := make([]Link, 0, len(s.links))
	for _, l := range s.links {
		ls = append(ls, l)
	}
	return snapshot{objects: objs, links: ls}
}

// commit applies one validated plan atomically under both locks' ordering:
// the engine lock is taken by callers, so only the store lock remains.
func (s *MemoryStore) commit(p *Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range p.Deleted {
		delete(s.objects, id)
	}
	for id := range p.RemovedLinks {
		delete(s.links, id)
	}
}
