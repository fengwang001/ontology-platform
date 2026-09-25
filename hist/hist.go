// Package hist manages many per-key histories and enforces the store-level
// rules: non-empty key, strictly increasing ingest time, per-key capacity.
// It depends only on ver; validation always precedes mutation.
package hist

import (
	"errors"
	"sync"

	"ontology/ver"
)

// Sentinel errors; every rejection leaves all state untouched.
var (
	ErrBadConfig          = errors.New("hist: maxVersions must be >= 1")
	ErrEmptyKey           = errors.New("hist: key must not be empty")
	ErrNonMonotonicIngest = errors.New("hist: ingest time must exceed the key's max In")
	ErrCapacity           = errors.New("hist: key reached its version capacity")
)

// Store is a concurrency-safe map from key to its version history.
type Store struct {
	mu   sync.RWMutex
	max  int
	keys map[string]*ver.History
}

// NewStore validates maxVersions and returns an empty store.
func NewStore(maxVersions int) (*Store, error) {
	if maxVersions < 1 {
		return nil, ErrBadConfig
	}
	return &Store{max: maxVersions, keys: make(map[string]*ver.History)}, nil
}

// Apply validates fully, then appends. Any rejection returns before mutation.
func (s *Store) Apply(key, value string, ev, in int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.keys[key]
	if h != nil {
		if maxIn, _ := h.MaxIn(); in <= maxIn {
			return ErrNonMonotonicIngest
		}
		if h.Len() >= s.max {
			return ErrCapacity
		}
	} else {
		h = ver.New()
		s.keys[key] = h // a fresh key cannot fail the checks above
	}
	h.Append(ver.Version{Value: value, Ev: ev, In: in})
	return nil
}

// Query runs q against key's history under a read lock.
func (s *Store) Query(key string, q func(*ver.History) (ver.Version, error)) (ver.Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.keys[key]
	if h == nil {
		return ver.Version{}, ver.ErrNotFound
	}
	return q(h)
}
