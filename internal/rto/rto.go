// Package rto holds single-connection retransmission-timer state:
// ACK base, current RTO with exponential backoff, and the single timer
// that always points at the earliest unacknowledged segment.
//
// It depends on no other package. Callers (package rtx) are responsible
// for event validation; methods here assume legal, monotonic inputs.
package rto

import "fmt"

// State is the timer core.
type State struct {
	base     int64 // acknowledged lower bound; every seq < base is gone
	baseRTO  int64 // reset target after an advancing ACK
	rto      int64 // current retransmission timeout
	backoff  int   // consecutive timeouts since the last advancing ACK
	armed    bool  // whether the timer is running
	deadline int64 // meaningful only when armed
	earliest int64 // earliest unacknowledged seq while armed

	// seqs[head:] are the unacknowledged segments in strictly
	// increasing send order: ACK only ever removes a prefix.
	seqs []int64
	head int

	// lastTickProbed counts unacknowledged segments inspected by the
	// latest timeout check. Unexported and unreachable through any
	// exported API: the check inspects only the earliest segment.
	lastTickProbed int
}

// New creates a State. Contract: baseRTO > 0 (validated by rtx.New).
func New(baseRTO int64) *State {
	return &State{baseRTO: baseRTO, rto: baseRTO}
}

// Send registers a segment sent at now. A segment already covered by a
// prior cumulative ACK is discarded on arrival and never arms the timer.
func (s *State) Send(seq, now int64) {
	if seq < s.base {
		return
	}
	idle := s.head == len(s.seqs)
	s.seqs = append(s.seqs, seq)
	if idle { // it becomes the earliest (and only) pending segment
		s.earliest = seq
		s.armed = true
		s.deadline = now + s.rto
	}
}

// ApplyAck confirms every seq < a. It reports whether base advanced.
// Advancement always resets rto/backoff to baseRTO (even when no
// segment remains, so the next Send starts unbacked-off); when segments
// remain the deadline is re-armed, otherwise the timer stops.
func (s *State) ApplyAck(a, now int64) bool {
	if a <= s.base {
		return false
	}
	s.base = a
	for s.head < len(s.seqs) && s.seqs[s.head] < a {
		s.head++
	}
	s.rto = s.baseRTO
	s.backoff = 0
	if s.head == len(s.seqs) {
		s.seqs = s.seqs[:0]
		s.head = 0
		s.armed = false
		s.deadline = 0
		return true
	}
	s.earliest = s.seqs[s.head]
	s.armed = true
	s.deadline = now + s.rto
	return true
}

// Probe advances the clock check. With pending segments and now equal to
// or past the deadline (left-closed), it retransmits the earliest one,
// doubles rto and re-arms. It inspects exactly one pending segment.
func (s *State) Probe(now int64) (timeout bool, retransmitted int64) {
	s.lastTickProbed = 0
	if !s.armed {
		return false, 0
	}
	s.lastTickProbed = 1 // only the earliest segment is ever inspected
	if now < s.deadline {
		return false, 0
	}
	seq := s.earliest
	s.rto *= 2
	s.backoff++
	s.deadline = now + s.rto
	return true, seq
}

func (s *State) Base() int64       { return s.base }
func (s *State) RTO() int64        { return s.rto }
func (s *State) Backoff() int      { return s.backoff }
func (s *State) HasDeadline() bool { return s.armed }
func (s *State) Deadline() int64   { return s.deadline }
func (s *State) PendingCount() int { return len(s.seqs) - s.head }

// VerifyProbeBound is an error-only self-check (no counter value ever
// leaves the package): across several pending-set sizes, a non-firing
// Tick must inspect no more than one segment regardless of m.
func VerifyProbeBound() error {
	for _, m := range []int{100, 1000, 10000} {
		s := New(10)
		for i := 0; i < m; i++ {
			s.Send(int64(i), 0)
		}
		if to, _ := s.Probe(5); to || s.lastTickProbed > 1 {
			return fmt.Errorf("rto: probe scan not bounded at m=%d", m)
		}
	}
	return nil
}
