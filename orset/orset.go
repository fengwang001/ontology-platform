// Package orset implements an observed-remove set (OR-Set) CRDT.
package orset

import "errors"

// Distinct, distinguishable rejection reasons.
var (
	ErrInvalidArgument = errors.New("orset: invalid argument")
	ErrInvalidReplica  = errors.New("orset: invalid replica id")
	ErrEmptyElement    = errors.New("orset: element must not be empty")
	ErrElementMissing  = errors.New("orset: element not present")
	ErrAddLimit        = errors.New("orset: add-record limit exceeded")
)

// Tag uniquely identifies a single add operation.
type Tag struct {
	Replica int
	Counter int64
}

// Replica is one OR-Set replica.
type Replica struct {
	id      int
	maxAdds int64

	mu       chan struct{}
	adds     map[Tag]string
	tomb     map[Tag]struct{}
	counter  int64
}

// Option configures a Replica at construction time.
type Option func(*Replica)

// WithAddLimit overrides the maximum number of add records a replica may
// accumulate (its own adds plus records learned through merges).
func WithAddLimit(n int64) Option {
	return func(r *Replica) {}
}

// NewReplica creates a replica with the given non-negative id.
func NewReplica(id int, opts ...Option) (*Replica, error) {
	return nil, nil
}

// Add inserts element, minting one fresh unique tag.
func (r *Replica) Add(element string) (Tag, error) {
	return Tag{}, nil
}

// Remove deletes element by tombstoning all of its currently live tags.
func (r *Replica) Remove(element string) error {
	return nil
}

// Merge imports all add records and tombstones from src into r.
func (r *Replica) Merge(src *Replica) error {
	return nil
}

// Contains reports whether element is currently live.
func (r *Replica) Contains(element string) (bool, error) {
	return false, nil
}

// Elements returns the sorted list of currently live elements.
func (r *Replica) Elements() ([]string, error) {
	return nil, nil
}

// Tags returns the live tags of element.
func (r *Replica) Tags(element string) ([]Tag, error) {
	return nil, nil
}

// ID returns the replica id.
func (r *Replica) ID() int {
	return 0
}
