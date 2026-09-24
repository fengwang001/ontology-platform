// Package kq holds the per-key state of the CDC applier:
// block flag, head event with attempt count, FIFO buffer, last Seq.
// It depends on no other package.
package kq

// Event is one upstream CDC event.
type Event struct {
	Key string
	Seq int64
}

// Queue is the state of a single Key.
//
// Invariants held here: when blocked is false there is no head and the
// buffer is empty only after a full drain (a freshly re-blocked queue may
// still hold buffered events). The head event is never counted in BufLen.
type Queue struct {
	lastSeq  int64
	blocked  bool
	head     Event
	attempts int
	buf      []Event
}

// New creates an empty per-key queue.
func New() *Queue { return &Queue{lastSeq: 0} }

// LastSeq returns the Seq of the last successfully submitted event.
func (q *Queue) LastSeq() int64 { return q.lastSeq }

// Blocked reports whether the key is currently blocked.
func (q *Queue) Blocked() bool { return q.blocked }

// BufLen is the number of buffered (non-head) events.
func (q *Queue) BufLen() int { return len(q.buf) }

// Head returns the blocking head event and its attempt count.
func (q *Queue) Head() (Event, int) { return q.head, q.attempts }

// BufCopy returns a copy of the FIFO buffer, oldest first.
func (q *Queue) BufCopy() []Event {
	out := make([]Event, len(q.buf))
	copy(out, q.buf)
	return out
}

// CommitSeq records that an event with seq was accepted by Submit.
func (q *Queue) CommitSeq(seq int64) { q.lastSeq = seq }

// Buffer appends e to the FIFO queue (key already blocked).
func (q *Queue) Buffer(e Event) { q.buf = append(q.buf, e) }

// Block makes e the head with one attempt and marks the key blocked.
// Used when an event's first attempt fails.
func (q *Queue) Block(e Event) {
	q.blocked = true
	q.head = e
	q.attempts = 1
}

// Retry records one more failed attempt on the head.
func (q *Queue) Retry() { q.attempts++ }

// ClearHead finalizes the head (applied or dead-lettered): the key is
// unblocked and the head slot is emptied. Buffered events are untouched.
func (q *Queue) ClearHead() {
	q.blocked = false
	q.head = Event{}
	q.attempts = 0
}

// Pop removes and returns the oldest buffered event.
func (q *Queue) Pop() (Event, bool) {
	if len(q.buf) == 0 {
		return Event{}, false
	}
	e := q.buf[0]
	q.buf = q.buf[1:]
	return e, true
}
