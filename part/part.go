// Package part models a single partition: its lifecycle state machine and the
// set of keys it owns. It has no dependencies and performs no locking; the
// router serializes all access.
package part

import "errors"

// State is the lifecycle state of a partition.
type State int

const (
	// Active accepts new assignments and writes.
	Active State = iota
	// Draining rejects new assignments/writes but still serves reads.
	Draining
	// Removed has been drained and migrated away; it owns no keys.
	Removed
)

// ErrIllegalTransition is returned when a lifecycle transition is invalid.
var ErrIllegalTransition = errors.New("part: illegal state transition")

// Partition is one partition's state plus its owned key set.
type Partition struct {
	state   State
	members map[string]struct{}
}

// New creates an Active, empty partition.
func New() *Partition {
	return &Partition{state: Active, members: make(map[string]struct{})}
}

// State reports the lifecycle state.
func (p *Partition) State() State { return p.state }

// BeginDrain moves Active to Draining.
func (p *Partition) BeginDrain() error {
	if p.state != Active {
		return ErrIllegalTransition
	}
	p.state = Draining
	return nil
}

// MarkRemoved moves Draining to Removed.
func (p *Partition) MarkRemoved() error {
	if p.state != Draining {
		return ErrIllegalTransition
	}
	p.state = Removed
	return nil
}

// Add registers a key as owned by this partition.
func (p *Partition) Add(key string) { p.members[key] = struct{}{} }

// Delete removes a key from the owned set.
func (p *Partition) Delete(key string) { delete(p.members, key) }

// Has reports whether the partition owns the key.
func (p *Partition) Has(key string) bool {
	_, ok := p.members[key]
	return ok
}

// Len is the number of owned keys. A Removed partition must always be empty.
func (p *Partition) Len() int { return len(p.members) }

// TakeAll removes and returns every owned key at once. Used by Migrate to move
// the whole membership atomically under the router's write lock.
func (p *Partition) TakeAll() []string {
	keys := make([]string, 0, len(p.members))
	for k := range p.members {
		keys = append(keys, k)
	}
	p.members = make(map[string]struct{})
	return keys
}
