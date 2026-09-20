package ontology

// IsNull reports, for attr, which entities lack the attribute entirely
// (missing) and which have it set to an explicit nil. Both groups are
// excluded from the equality index; an empty-string value is a normal
// value and appears in neither group. Each returned slice is sorted and
// independent of internal state.
func (s *Store) IsNull(attr string) (missing, nilIDs []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ix, ok := s.indexes[attr]; ok {
		missingSet := make(map[string]struct{})
		for id := range s.entities {
			if _, ok := ix.present[id]; ok {
				continue
			}
			if _, ok := ix.nilIDs[id]; ok {
				continue
			}
			missingSet[id] = struct{}{}
		}
		return sortedIDs(missingSet), sortedIDs(ix.nilIDs)
	}
	// Non-indexed attribute: derive the answer from the entity store.
	missingSet := make(map[string]struct{})
	nilSet := make(map[string]struct{})
	for id, props := range s.entities {
		v, ok := props[attr]
		switch {
		case !ok:
			missingSet[id] = struct{}{}
		case v == nil:
			nilSet[id] = struct{}{}
		}
	}
	return sortedIDs(missingSet), sortedIDs(nilSet)
}

// NullCount returns how many entities have attr explicitly set to nil.
func (s *Store) NullCount(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ix, ok := s.indexes[attr]; ok {
		return len(ix.nilIDs)
	}
	n := 0
	for _, props := range s.entities {
		if v, ok := props[attr]; ok && v == nil {
			n++
		}
	}
	return n
}

// MissingCount returns how many entities do not have attr at all.
func (s *Store) MissingCount(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ix, ok := s.indexes[attr]; ok {
		return len(s.entities) - len(ix.present) - len(ix.nilIDs)
	}
	n := 0
	for _, props := range s.entities {
		if _, ok := props[attr]; !ok {
			n++
		}
	}
	return n
}

// IndexedRowCount returns how many entities carry a non-nil value for
// attr and are therefore present in its equality index. For any attr,
// IndexedRowCount + MissingCount + NullCount == TotalRows.
func (s *Store) IndexedRowCount(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ix, ok := s.indexes[attr]; ok {
		return len(ix.present)
	}
	n := 0
	for _, props := range s.entities {
		if v, ok := props[attr]; ok && v != nil {
			n++
		}
	}
	return n
}
