package ontology

// ring is a fixed-capacity FIFO buffer of events. It is not safe for
// concurrent use; all access happens while the dispatcher lock is held.
type ring struct {
	buf  []Event
	head int // index of the oldest event
	size int
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]Event, capacity)}
}

func (r *ring) len() int { return r.size }

// push appends an event to the tail. Caller must ensure r.size < cap.
func (r *ring) push(e Event) {
	c := len(r.buf)
	i := (r.head + r.size) % c
	r.buf[i] = e
	r.size++
}

// pop removes and returns the oldest event. The boolean is false when empty.
func (r *ring) pop() (Event, bool) {
	if r.size == 0 {
		return Event{}, false
	}
	e := r.buf[r.head]
	r.buf[r.head] = Event{}
	r.head = (r.head + 1) % len(r.buf)
	r.size--
	return e, true
}

// peekOldest returns the oldest event without removing it.
func (r *ring) peekOldest() Event { return r.buf[r.head] }

func (r *ring) clear() {
	for i := range r.buf {
		r.buf[i] = Event{}
	}
	r.head = 0
	r.size = 0
}
