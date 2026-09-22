package slot

import "time"

// ErrInvalidTransition is returned when a lifecycle move is not legal.
var ErrInvalidTransition = newError("slot: invalid state transition")

// Slot is one in-flight request position.
type Slot struct {
	index    uint32
	gen      uint32
	state    State
	deadline time.Time
}

// New creates a fresh slot bound to the given index.
func New(index uint32) *Slot { return &Slot{index: index} }

// Index reports the slot's fixed position.
func (s *Slot) Index() uint32 { return s.index }

// Generation reports the current request generation occupying the slot.
func (s *Slot) Generation() uint32 { return s.gen }

// State reports the current lifecycle state.
func (s *Slot) State() State { return s.state }

// Deadline reports the pending request deadline; it is zero once settled.
func (s *Slot) Deadline() time.Time { return s.deadline }

// Begin opens a new request generation with the given deadline.
func (s *Slot) Begin(deadline time.Time) ID {
	s.gen++
	s.state = Pending
	s.deadline = deadline
	return Encode(s.index, s.gen)
}

// Complete moves a pending slot to Completed.
func (s *Slot) Complete() error { return s.settle(Completed) }

// TimeOut moves a pending slot to TimedOut.
func (s *Slot) TimeOut() error { return s.settle(TimedOut) }

// Cancel moves a pending slot to Canceled.
func (s *Slot) Cancel() error { return s.settle(Canceled) }

func (s *Slot) settle(target State) error {
	if s.state != Pending {
		return ErrInvalidTransition
	}
	s.state = target
	s.deadline = time.Time{}
	return nil
}

// IsPendingAt reports whether the slot is still pending at now.
func (s *Slot) IsPendingAt(now time.Time) bool {
	return s.state == Pending && now.Before(s.deadline)
}

// ExpiredAt reports whether a pending slot reached its deadline at now.
// Expiration is left-closed, right-open: now == deadline is expired.
func (s *Slot) ExpiredAt(now time.Time) bool {
	return s.state == Pending && !s.deadline.IsZero() && !now.Before(s.deadline)
}

// Remaining reports time left until the deadline at now.
func (s *Slot) Remaining(now time.Time) time.Duration {
	if s.state != Pending {
		return 0
	}
	d := s.deadline.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
