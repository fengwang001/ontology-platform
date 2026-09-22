package slot

import "time"

// Allocate moves a vacant slot to Waiting, bumps its epoch and records the
// deadline. It returns the new epoch. A slot that is still Waiting cannot be
// reallocated: callers must finish or release it first.
func (s *Slot) Allocate(deadline time.Time) (epoch uint64, ok bool) {
	if s.state == Waiting {
		return s.epoch, false
	}
	s.epoch++
	s.state = Waiting
	s.deadline = deadline
	return s.epoch, true
}

// Complete moves a waiting slot to Completed. It succeeds only when the slot
// is Waiting with the given epoch; this is the exactly-once guard.
func (s *Slot) Complete(epoch uint64) bool {
	return s.finish(epoch, Completed)
}

// Cancel moves a waiting slot to Canceled.
func (s *Slot) Cancel(epoch uint64) bool {
	return s.finish(epoch, Canceled)
}

// Expire moves a waiting slot to TimedOut. It does not take an epoch because
// expiration is driven by the holder, not by an external token.
func (s *Slot) Expire() bool {
	if s.state != Waiting {
		return false
	}
	s.state = TimedOut
	return true
}

// Release returns a finished slot to Vacant so its ID can be reallocated.
// Releasing a waiting slot is rejected: it must be finished first.
func (s *Slot) Release() bool {
	if s.state == Waiting || s.state == Vacant {
		return s.state != Waiting
	}
	s.state = Vacant
	return true
}

// Matches reports whether the slot is currently held by the given epoch.
func (s *Slot) Matches(epoch uint64) bool {
	return s.state == Waiting && s.epoch == epoch
}

// Expired reports whether a waiting slot's deadline has been reached.
// Timeout is left-closed, right-open: now == deadline counts as timed out.
func (s *Slot) Expired(now time.Time) bool {
	return s.state == Waiting && !now.Before(s.deadline)
}

func (s *Slot) finish(epoch uint64, to State) bool {
	if s.state != Waiting || s.epoch != epoch {
		return false
	}
	s.state = to
	return true
}
