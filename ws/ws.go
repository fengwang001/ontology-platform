// Package ws delays classification of trailing spaces and tabs.
package ws

// Delayer buffers a run of spaces and tabs until a line end proves it trailing.
type Delayer struct {
	buf   []byte
	start int
	limit int
}

// New creates a delayer with a maximum buffered byte count.
func New(limit int) *Delayer {
	if limit <= 0 {
		limit = 1 << 20
	}
	return &Delayer{limit: limit}
}

// Add appends b when it is a space or tab. Other bytes must call Keep/Drop.
func (d *Delayer) Add(b byte, offset int) (bool, int) {
	if !Is(b) {
		return true, 0
	}
	if len(d.buf) == 0 {
		d.start = offset
	}
	if len(d.buf) >= d.limit {
		return false, d.start
	}
	d.buf = append(d.buf, b)
	return true, d.start
}

// Keep returns and clears buffered whitespace because a non-whitespace byte follows.
func (d *Delayer) Keep() []byte {
	out := d.buf
	d.buf = nil
	return out
}

// Drop discards buffered whitespace because a line end or stream end follows.
func (d *Delayer) Drop() (int, int) {
	start, end := d.start, d.start+len(d.buf)
	d.buf = nil
	d.start = 0
	return start, end
}

// Pending returns the currently buffered bytes without clearing them.
func (d *Delayer) Pending() []byte { return d.buf }

// Start returns the original offset of the first buffered byte.
func (d *Delayer) Start() int { return d.start }

// Active reports whether whitespace is buffered.
func (d *Delayer) Active() bool { return len(d.buf) > 0 }

// Reset clears all state.
func (d *Delayer) Reset() { d.buf, d.start = nil, 0 }

// Is reports whether b is trailing-run whitespace.
func Is(b byte) bool { return b == ' ' || b == '\t' }
