package sourcemap

import "sync"

type Service struct {
	mu       sync.RWMutex
	mappings map[string]*IndexedMapping
}

// NewService creates an empty in-memory mapping registry.
func NewService() *Service {
	return &Service{mappings: make(map[string]*IndexedMapping)}
}

func validateName(name string) error {
	if name == "" {
		return invalidArgument("mapping name must not be empty")
	}
	return nil
}

// Register validates, canonicalizes and stores a mapping under a unique name.
func (s *Service) Register(name string, mapping Mapping) error {
	if err := validateName(name); err != nil {
		return err
	}
	index, err := newIndexedMapping(mapping)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mappings[name]; exists {
		return duplicateName("mapping name already exists")
	}
	s.mappings[name] = index
	return nil
}

// Compose creates resultName from two mappings already present in the registry.
func (s *Service) Compose(intermediateToSourceName, finalToIntermediateName, resultName string) error {
	if err := validateName(intermediateToSourceName); err != nil {
		return err
	}
	if err := validateName(finalToIntermediateName); err != nil {
		return err
	}
	if err := validateName(resultName); err != nil {
		return err
	}

	s.mu.RLock()
	m1, m1Exists := s.mappings[intermediateToSourceName]
	m2, m2Exists := s.mappings[finalToIntermediateName]
	s.mu.RUnlock()
	if !m1Exists || !m2Exists {
		return notFound("source mapping name not found")
	}

	composed, err := Compose(m2.toMapping(), m1.toMapping())
	if err != nil {
		return err
	}
	result, err := newIndexedMapping(composed)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mappings[resultName]; exists {
		return duplicateName("result mapping name already exists")
	}
	s.mappings[resultName] = result
	return nil
}

// Lookup finds a registered mapping and performs one point query.
func (s *Service) Lookup(name string, line, column uint64) (LookupResult, error) {
	if err := validateName(name); err != nil {
		return LookupResult{}, err
	}
	if line > MaxCoordinate || column > MaxCoordinate {
		return LookupResult{}, invalidArgument("query position exceeds 10^9")
	}

	s.mu.RLock()
	mapping, exists := s.mappings[name]
	s.mu.RUnlock()
	if !exists {
		return LookupResult{}, notFound("mapping name not found")
	}
	return mapping.Lookup(line, column)
}

// Get returns a defensive copy of the registered canonical mapping.
func (s *Service) Get(name string) (Mapping, bool) {
	s.mu.RLock()
	mapping, exists := s.mappings[name]
	s.mu.RUnlock()
	if !exists {
		return Mapping{}, false
	}
	return mapping.toMapping(), true
}
