// Package history implements an append-only event log for one workflow
// instance. Events are numbered from 1 with no gaps.
package history

import "errors"

// ErrSealed is returned when appending to a log that already has a C event.
var ErrSealed = errors.New("history: log is sealed by a close event")

// Kind is the type of a logged event.
type Kind int

const (
	// UpdateAccepted records that an update was accepted: U(uid, delta).
	UpdateAccepted Kind = iota
	// UpdateApplied records that the head queued update was applied: A(uSeq).
	UpdateApplied
	// Closed records that the instance was closed: C. At most one per log.
	Closed
)

// Event is one entry in the log. Seq is assigned by Log.Append.
type Event struct {
	Kind  Kind
	Seq   int64  // 1-based position in the log
	UID   string // set for UpdateAccepted
	Delta int64  // set for UpdateAccepted
	Ref   int64  // set for UpdateApplied: seq of the applied U event
}

// Log is the append-only history of a single instance.
type Log struct {
	events []Event
	closed bool
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Len returns the number of events (also the next seq minus one).
func (l *Log) Len() int { return len(l.events) }

// IsClosed reports whether a C event has been appended.
func (l *Log) IsClosed() bool { return l.closed }

// Events returns a copy of the first n events (n <= Len).
func (l *Log) Events(n int) []Event {
	out := make([]Event, n)
	copy(out, l.events[:n])
	return out
}

// AppendUpdate appends U(uid, delta) and returns its seq.
func (l *Log) AppendUpdate(uid string, delta int64) (int64, error) {
	return l.append(Event{Kind: UpdateAccepted, UID: uid, Delta: delta})
}

// AppendApplied appends A(ref) and returns its seq.
func (l *Log) AppendApplied(ref int64) (int64, error) {
	return l.append(Event{Kind: UpdateApplied, Ref: ref})
}

// AppendClosed appends C and returns its seq.
func (l *Log) AppendClosed() (int64, error) {
	return l.append(Event{Kind: Closed})
}

// append seals the log against further writes once a C event is added and
// assigns the next contiguous seq.
func (l *Log) append(e Event) (int64, error) {
	if l.closed {
		return 0, ErrSealed
	}
	e.Seq = int64(len(l.events)) + 1
	l.events = append(l.events, e)
	if e.Kind == Closed {
		l.closed = true
	}
	return e.Seq, nil
}
