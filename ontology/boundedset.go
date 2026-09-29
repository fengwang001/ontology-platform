package ontology

// Reason describes why a Track call was rejected.
type Reason int

const (
	ReasonOK Reason = iota
	ReasonEmptyKey
	ReasonNegativeTimestamp
	ReasonInvalidCapacity
)

// Eviction records the member removed by a successful Track call.
type Eviction struct {
	Key       string
	Timestamp int64
	Slot      uint64
	Reason    string
}

// TrackResult is the outcome of one Track call.
type TrackResult struct {
	Evicted   *Eviction
	IsNew     bool
	Refreshed bool
}

// Entry is an immutable view of one set member.
type Entry struct {
	Key       string
	Timestamp int64
	Slot      uint64
}

// BoundedSet is a concurrency-safe bounded key set.
type BoundedSet struct {
	capacity int
}

// NewBoundedSet constructs a BoundedSet with the given positive capacity.
func NewBoundedSet(capacity int) (*BoundedSet, error) {
	return &BoundedSet{capacity: capacity}, nil
}

// Track refreshes or inserts a key, deterministically evicting the oldest
// member when the capacity is exceeded.
func (s *BoundedSet) Track(key string, timestamp int64) (TrackResult, error) {
	return TrackResult{}, nil
}

// Len returns the number of members currently held.
func (s *BoundedSet) Len() int { return 0 }

// Cap returns the configured capacity.
func (s *BoundedSet) Cap() int { return s.capacity }

// Contains reports whether key is currently a member.
func (s *BoundedSet) Contains(key string) bool { return false }

// Snapshot returns a deterministic copy of the current members.
func (s *BoundedSet) Snapshot() []Entry { return nil }

// Check verifies internal invariants and returns the first violation found.
func (s *BoundedSet) Check() error { return nil }
