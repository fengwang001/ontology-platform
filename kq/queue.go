// Package kq holds the per-key state of the CDC applier:
// block flag, head event with attempt count, FIFO buffer, last accepted Seq.
// It depends on no other package and is deliberately free of locking and
// failure policy; the applier owns both.
package kq

// Event is one upstream CDC record.
type Event struct {
	Key string
	Seq int64
}

// Queue is the state machine for a single key. The head event, while the
// queue is blocked, is never counted in BufferLen.
type Queue struct {
	lastSeq  int64
	hasLast  bool
	blocked  bool
	head     Event
	attempts int
	buf      []Event
}

// Accepts reports whether seq is strictly greater than the last successfully
// submitted seq for this key.
func (q *Queue) Accepts(seq int64) bool { return !q.hasLast || seq > q.lastSeq }

// NoteSubmitted records the seq of an accepted Submit (buffered or tried).
func (q *Queue) NoteSubmitted(seq int64) { q.lastSeq, q.hasLast = seq, true }

// LastSeq returns the last accepted seq and whether one exists.
func (q *Queue) LastSeq() (int64, bool) { return q.lastSeq, q.hasLast }

// Blocked reports whether the key is blocked on its head event.
func (q *Queue) Blocked() bool { return q.blocked }

// BufferLen is the number of buffered events; the head is not counted.
func (q *Queue) BufferLen() int { return len(q.buf) }

// Head returns the blocking head event and its attempt count.
func (q *Queue) Head() (Event, int) { return q.head, q.attempts }

// Block puts e at the head after its first failed attempt.
func (q *Queue) Block(e Event) { q.blocked, q.head, q.attempts = true, e, 1 }

// Retry records one more attempt on the head and returns its new count.
func (q *Queue) Retry() int { q.attempts++; return q.attempts }

// Unblock clears the block flag. The buffer is left untouched.
func (q *Queue) Unblock() { q.blocked, q.head, q.attempts = false, Event{}, 0 }

// Append enqueues e at the tail of the FIFO buffer.
func (q *Queue) Append(e Event) { q.buf = append(q.buf, e) }

// PopFront removes and returns the oldest buffered event.
func (q *Queue) PopFront() (Event, bool) {
	if len(q.buf) == 0 {
		return Event{}, false
	}
	e := q.buf[0]
	q.buf = q.buf[1:] // keep FIFO; cap shrinks with the window, appends reallocate
	return e, true
}

// Buffered returns a copy of the FIFO buffer, oldest first; head excluded.
func (q *Queue) Buffered() []Event { return append([]Event(nil), q.buf...) }
