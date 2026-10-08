package dtc

// freezeFrameSlot is the single vehicle-wide freeze frame slot.
//
// A DTC captures the slot when it enters the pending state. If the
// slot is already owned by another DTC, the newcomer replaces the
// owner only when its severity is strictly greater; on equal severity
// the current owner keeps the slot. The slot is released only when the
// owner is fully erased (auto-clear) or on a tester clear; it is never
// handed over to another DTC automatically.
type freezeFrameSlot struct {
	frame    FreezeFrame
	severity int // severity of the current owner
}

// tryAcquire lets DTC id with the given severity compete for the slot.
// env holds the environment data to store if the slot is captured.
func (s *freezeFrameSlot) tryAcquire(id string, severity int, env FreezeFrame) {
	if s.frame.Occupied && severity <= s.severity {
		return // occupied by an equally or more severe DTC: keep as is
	}
	env.Occupied = true
	env.Owner = id
	s.frame = env
	s.severity = severity
}

// releaseIfOwner frees the slot when id currently owns it.
func (s *freezeFrameSlot) releaseIfOwner(id string) {
	if s.frame.Occupied && s.frame.Owner == id {
		s.reset()
	}
}

// reset frees the slot unconditionally (tester clear).
func (s *freezeFrameSlot) reset() {
	s.frame = FreezeFrame{}
	s.severity = 0
}

func (s *freezeFrameSlot) ownedBy(id string) bool {
	return s.frame.Occupied && s.frame.Owner == id
}
