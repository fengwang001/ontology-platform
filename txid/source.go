package txid

import "sync"

// memSource is the default thread-safe monotonic allocator. It holds only a
// counter in process memory; there is no clock and no persistence.
type memSource struct {
	mu   sync.Mutex
	next ID
}

func (s *memSource) Allocate() (ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	if id == Invalid {
		id = 1
	}
	if id == ^ID(0) {
		return Invalid, ErrExhausted
	}
	s.next = id + 1
	return id, nil
}

func (s *memSource) Peek() (ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	if id == Invalid {
		id = 1
	}
	if id == ^ID(0) {
		return Invalid, ErrExhausted
	}
	return id, nil
}

type funcSource struct {
	allocate func() (ID, error)
	peek     func() (ID, error)
}

func (s *funcSource) Allocate() (ID, error) { return s.allocate() }
func (s *funcSource) Peek() (ID, error)     { return s.peek() }
