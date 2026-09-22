// Package slot models a single in-flight request slot as a small state
// machine: vacant -> waiting -> one of completed/timed out/canceled.
//
// A slot carries an epoch that is bumped every time the slot is allocated.
// The epoch lets callers tell a late reply for an old generation apart from
// a reply owed to the generation currently holding the ID.
package slot

import "time"

// State is the lifecycle state of one slot.
type State uint8

const (
	// Vacant means the slot is free (either never used or released after
	// a previous generation finished).
	Vacant State = iota
	// Waiting means a request is in flight awaiting its reply.
	Waiting
	// Completed means the in-flight request was satisfied exactly once.
	Completed
	// TimedOut means the request exceeded its deadline.
	TimedOut
	// Canceled means the request was canceled by the caller.
	Canceled
)

// Slot is a single in-flight slot. The zero value is a usable vacant slot.
type Slot struct {
	state    State
	epoch    uint64
	deadline time.Time
}

// New returns a fresh vacant slot.
func New() *Slot {
	return &Slot{}
}

// State reports the current state.
func (s *Slot) State() State { return s.state }

// Epoch reports the generation tag of the current (or, when vacant, the most
// recent) allocation. It is zero before the first allocation.
func (s *Slot) Epoch() uint64 { return s.epoch }

// Deadline reports the deadline of the current allocation; it is meaningful
// only while Waiting.
func (s *Slot) Deadline() time.Time { return s.deadline }
