// Package timer defines the timer handle and its state machine
// (Pending -> Fired | Cancelled). Not goroutine-safe; the scheduler
// synchronizes all access.
package timer

import "ontology/slot"

// State is the lifecycle state of a Timer.
type State uint8

const (
	Pending State = iota
	Fired
	Cancelled
)

// Timer is a scheduled element. Seq is a monotone registration sequence
// number used to order same-tick firings by Add order. gen is bumped by
// Reset to invalidate already-collected firing candidates.
type Timer struct {
	ID       uint64
	Seq      uint64
	Deadline int64
	Payload  any

	state   State
	gen     uint64
	ref     *slot.Slot
	level   int
	slotIdx int
}

// New creates a Pending timer.
func New(id, seq uint64, deadline int64, payload any) *Timer {
	return &Timer{
		ID: id, Seq: seq, Deadline: deadline, Payload: payload,
		level: -1, slotIdx: -1,
	}
}

// State returns the current state.
func (t *Timer) State() State { return t.state }

// Gen returns the current generation.
func (t *Timer) Gen() uint64 { return t.gen }

// BumpGen invalidates firing candidates collected before a Reset.
func (t *Timer) BumpGen() { t.gen++ }

// Fire transitions Pending -> Fired; reports whether the transition happened.
func (t *Timer) Fire() bool {
	if t.state != Pending {
		return false
	}
	t.state = Fired
	return true
}

// Cancel transitions Pending -> Cancelled; reports whether it happened.
func (t *Timer) Cancel() bool {
	if t.state != Pending {
		return false
	}
	t.state = Cancelled
	return true
}

// Attach records the timer's location inside a wheel slot.
func (t *Timer) Attach(ref *slot.Slot, level, slotIdx int) {
	t.ref, t.level, t.slotIdx = ref, level, slotIdx
}

// Detach removes the timer from its slot, if attached to one.
func (t *Timer) Detach() {
	if t.ref != nil {
		t.ref.Remove(t.ID)
	}
	t.ref, t.level, t.slotIdx = nil, -1, -1
}

// Location returns the wheel level and slot index, or (-1, -1).
func (t *Timer) Location() (level, slotIdx int) { return t.level, t.slotIdx }
