package export

import "sync"

// CheckpointStore stores, per independent export link, the last confirmed end.
type CheckpointStore interface {
	Load(link string) (Position, error)
	Save(link string, end Position) error
}

// Corrupt signals that the checkpoint medium is unreadable or ambiguous. The
// component translates it into KindCheckpointUnreadable and re-derives a
// safe start from history instead of trusting any value.
var Corrupt = corrupterr{}

type corrupterr struct{}

func (corrupterr) Error() string { return "export: checkpoint medium unreadable" }

// MemCheckpoint is a map-backed CheckpointStore. Flip Corrupt[link] to make
// that link's checkpoint unreadable (medium damage, torn write, ambiguity).
type MemCheckpoint struct {
	mu      sync.Mutex
	pos     map[string]Position
	Corrupt map[string]bool
}

// NewMemCheckpoint creates an empty in-memory checkpoint store.
func NewMemCheckpoint() *MemCheckpoint {
	return &MemCheckpoint{pos: map[string]Position{}, Corrupt: map[string]bool{}}
}

func (s *MemCheckpoint) Load(link string) (Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Corrupt[link] {
		return 0, Corrupt
	}
	return s.pos[link], nil
}

func (s *MemCheckpoint) Save(link string, end Position) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Corrupt[link] {
		return Corrupt
	}
	s.pos[link] = end
	return nil
}

// Repair clears the unreadable flag after an operator or the component has
// re-derived and persisted a trustworthy start.
func (s *MemCheckpoint) Repair(link string, at Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Corrupt, link)
	s.pos[link] = at
}
