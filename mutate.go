package ontology

// Upsert inserts a new entity or fully replaces the attributes of an
// existing one. The attrs map is copied, so later caller mutations do
// not affect the store. Repeated Upsert with the same ID keeps a
// single row and keeps all index counts exact.
func (s *Store) Upsert(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if old, ok := s.ents[id]; ok {
		s.removeLocked(id, old)
	} else {
		s.pos[id] = len(s.ids)
		s.ids = append(s.ids, id)
	}

	cp := make(map[string]any, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	s.ents[id] = cp
	s.addLocked(id, cp)
}

// Delete removes the entity and every index entry referencing it.
// Deleting an unknown ID is a no-op.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, ok := s.ents[id]
	if !ok {
		return
	}
	s.removeLocked(id, old)
	delete(s.ents, id)

	i := s.pos[id]
	last := s.ids[len(s.ids)-1]
	s.ids[i] = last
	s.pos[last] = i
	s.ids = s.ids[:len(s.ids)-1]
	delete(s.pos, id)
}

// addLocked inserts id into every index derived from attrs.
// Missing attributes and nil values never enter the equality index.
func (s *Store) addLocked(id string, attrs map[string]any) {
	for _, a := range s.attrs {
		v, ok := attrs[a]
		if !ok {
			continue
		}
		s.present[a][id] = struct{}{}
		if v == nil {
			s.nilSet[a][id] = struct{}{}
			continue
		}
		key, _ := keyOf(v)
		setAdd(s.index[a], key, id)
	}
}

// removeLocked undoes addLocked for the previous attribute set.
func (s *Store) removeLocked(id string, attrs map[string]any) {
	for _, a := range s.attrs {
		v, ok := attrs[a]
		if !ok {
			continue
		}
		delete(s.present[a], id)
		if v == nil {
			delete(s.nilSet[a], id)
			continue
		}
		key, _ := keyOf(v)
		setRemove(s.index[a], key, id)
	}
}
