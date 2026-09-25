// Package hb holds a single stream's heartbeat state: the last accepted
// heartbeat timestamp and the active/idle/dead classification derived from
// its age. It depends on no other package in this module.
package hb

import "errors"

const (
	// Timeout is the active boundary: age <= Timeout is active.
	Timeout int64 = 10
	// IdleWindow is the idle boundary: Timeout < age <= IdleWindow is idle,
	// age > IdleWindow is dead. Intervals are left-closed/right-open.
	IdleWindow int64 = 30
)

// Sentinel errors. All rejection paths return one of these so callers can
// discriminate failures with errors.Is.
var (
	ErrNegativeTime = errors.New("hb: negative timestamp or now")
	ErrClockBack    = errors.New("hb: clock moved backwards: now < lastHb")
	ErrStale        = errors.New("hb: stale heartbeat ignored: ts < lastHb")
	ErrAbsent       = errors.New("hb: stream has never sent a heartbeat")
)

// State is the liveness state of one stream at one clock value.
type State int

const (
	Absent State = iota // never received a heartbeat
	Active              // age <= Timeout
	Idle                // Timeout < age <= IdleWindow
	Dead                // age > IdleWindow
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Idle:
		return "idle"
	case Dead:
		return "dead"
	default:
		return "absent"
	}
}

// Stream is one stream's lastHb. Zero value is a valid never-seen stream.
type Stream struct {
	last int64
	seen bool
}

// NewStream returns an absent stream.
func NewStream() *Stream { return &Stream{} }

// Seen reports whether at least one heartbeat has been accepted.
func (s *Stream) Seen() bool { return s.seen }

// Last returns the last accepted timestamp and whether one exists.
func (s *Stream) Last() (int64, bool) { return s.last, s.seen }

// Observe applies rule Heartbeat: ts < 0 is rejected; ts < lastHb is a stale
// heartbeat and is ignored (lastHb unchanged); otherwise lastHb becomes ts.
// lastHb is therefore monotonic non-decreasing.
func (s *Stream) Observe(ts int64) error {
	if ts < 0 {
		return ErrNegativeTime
	}
	if s.seen && ts < s.last {
		return ErrStale
	}
	s.last, s.seen = ts, true
	return nil
}

// Age returns now - lastHb. It rejects negative now and clock rollback
// (now < lastHb); both leave the receiver untouched.
func (s *Stream) Age(now int64) (int64, error) {
	if !s.seen {
		return 0, ErrAbsent
	}
	if now < 0 {
		return 0, ErrNegativeTime
	}
	if now < s.last {
		return 0, ErrClockBack
	}
	return now - s.last, nil
}

// Classify maps a non-negative age to a state using left-closed/right-open
// boundaries: age==Timeout is active, age==IdleWindow is idle, dead starts
// at age IdleWindow+1.
func Classify(age int64) State {
	switch {
	case age <= Timeout:
		return Active
	case age <= IdleWindow:
		return Idle
	default:
		return Dead
	}
}

// StateAt combines Age and Classify.
func (s *Stream) StateAt(now int64) (State, error) {
	age, err := s.Age(now)
	if err != nil {
		return Absent, err
	}
	return Classify(age), nil
}
