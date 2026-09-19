package rollback

import (
	"errors"
	"sync"
)

// Object is the single in-memory object kind handled by the store.
type Object struct {
	ID    string
	Count int
}

// Store is a tiny in-memory key/value store whose objects only carry an
// integer Count field. Once the store is marked polluted every mutating call
// is rejected until an explicit Reset.
type Store struct {
	mu sync.Mutex

	objects map[string]*Object

	polluted     bool
	pollutedStep int
}

// NewStore returns an empty, clean store.
func NewStore() *Store {
	return &Store{
		objects:      make(map[string]*Object),
		pollutedStep: 0,
	}
}

// Add adds delta to the Count of id (creating the object if absent). It is a
// mutating operation and is therefore rejected after pollution.
func (s *Store) Add(id string, delta int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.polluted {
		return 0, s.pollutionErrorLocked()
	}
	obj := s.objects[id]
	if obj == nil {
		obj = &Object{ID: id}
		s.objects[id] = obj
	}
	obj.Count += delta
	return obj.Count, nil
}

// Set overwrites the Count of id. It is rejected after pollution.
func (s *Store) Set(id string, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.polluted {
		return s.pollutionErrorLocked()
	}
	obj := s.objects[id]
	if obj == nil {
		obj = &Object{ID: id}
		s.objects[id] = obj
	}
	obj.Count = count
	return nil
}

// Get returns the Count of id (0 when absent). Reads stay available after
// pollution so callers can still inspect the damage.
func (s *Store) Get(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj := s.objects[id]
	if obj == nil {
		return 0
	}
	return obj.Count
}

// MarkPolluted marks the store polluted and records the earliest pollution
// point. Repeated calls never overwrite an earlier (smaller) step number and
// never clear the state.
func (s *Store) MarkPolluted(step int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.polluted || step < s.pollutedStep {
		s.pollutedStep = step
	}
	s.polluted = true
}

// Polluted reports whether the store is currently polluted.
func (s *Store) Polluted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.polluted
}

// PollutionStep returns the step number of the earliest pollution point, or
// 0 when the store is clean.
func (s *Store) PollutionStep() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.pollutedStep
}

// IsPolluted reports whether err (or any wrapped error) is a pollution error.
func IsPolluted(err error) bool {
	return errors.Is(err, ErrPolluted)
}

// Reset clears every object and the pollution state. It is the only way to
// recover a polluted store.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.objects = make(map[string]*Object)
	s.polluted = false
	s.pollutedStep = 0
}

func (s *Store) pollutionErrorLocked() error {
	return &PollutionError{Step: s.pollutedStep}
}
