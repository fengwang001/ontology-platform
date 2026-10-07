package audit

// Test-only corruption hooks. Files named *_test.go are never shipped in
// production builds, so these deliberately mutating helpers exist only while
// running the test binary.

// TamperAudit mutates one stored audit field in place without rehashing.
func (s *Service) TamperAudit(id string, mutate func(*AuditRecord)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.store.auditsByID[id]
	if !ok {
		return false
	}
	mutate(&a)
	s.store.auditsByID[id] = a
	return true
}

// TamperVersion mutates one stored rule version in place without rehashing.
func (s *Service) TamperVersion(id string, mutate func(*RuleVersion)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.store.versionsByID[id]
	if !ok {
		return false
	}
	mutate(&v)
	s.store.versionsByID[id] = v
	return true
}

// TamperCorrection mutates one stored correction in place without rehashing.
func (s *Service) TamperCorrection(id string, mutate func(*Correction)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.store.corrByID[id]
	if !ok {
		return false
	}
	mutate(&c)
	s.store.corrByID[id] = c
	for i := range s.store.corrections {
		if s.store.corrections[i].ID == id {
			s.store.corrections[i] = c
		}
	}
	return true
}

// DeleteVersion simulates loss of a version.
func (s *Service) DeleteVersion(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.store.versionsByID[id]; !ok {
		return false
	}
	delete(s.store.versionsByID, id)
	for i, v := range s.store.versionOrder {
		if v == id {
			s.store.versionOrder = append(s.store.versionOrder[:i], s.store.versionOrder[i+1:]...)
			break
		}
	}
	return true
}

// MeasureVersion exposes the byte accounting used by replay.
func MeasureVersion(v RuleVersion) int64 { return measureVersionBytes(v) }
