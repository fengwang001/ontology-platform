package ontology

// snapshot is a deep copy of the whole store, taken before pre-hooks run so
// that any write a hook manages to smuggle in can be fully reverted.
type snapshot struct {
	objects   map[string]*Object
	relations map[string]*Relation
}

func (s *Store) snapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objs := make(map[string]*Object, len(s.objects))
	for id, o := range s.objects {
		cp := copyObject(o)
		objs[id] = &cp
	}
	rels := make(map[string]*Relation, len(s.relations))
	for id, r := range s.relations {
		cp := *r
		rels[id] = &cp
	}
	return snapshot{objects: objs, relations: rels}
}

func (s *Store) restore(snap snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = snap.objects
	s.relations = snap.relations
	s.version++
}
