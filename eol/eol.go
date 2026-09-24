// Package eol identifies line endings (\r\n, lone \r, \n) across stream cuts.
// Package eol identifies line endings (\r\n, lone \r, \n) across stream cuts.
package eol

// Event is one normalized line ending. Src is the original byte range that
// produced it: a lone \r is 1 byte (deleted), a \r\n pair is 2 bytes (the \r
// is deleted, the \n absorbed), a bare \n is 1 byte copied verbatim.
type Event struct {
	Src [2]int
}

// Decoder is a streaming line-ending scanner. The only cross-call ambiguity is
// a trailing \r that may be the first half of a \r\n pair.
type Decoder struct {
	pending   bool
	pendingAt int
}

// New returns a zero-ready Decoder.
func New() *Decoder { return &Decoder{} }

// Pending reports whether the decoder holds a trailing \r awaiting resolution.
func (d *Decoder) Pending() bool { return d.pending }

// Reset clears the pending state.
func (d *Decoder) Reset() { d.pending = false }

// Scan feeds byte b at absolute original offset pos. advance is 0 when b must
// be re-fed (a pending lone \r is resolved first). A non-nil Event marks one
// line ending; the caller emits a single \n for it.
func (d *Decoder) Scan(b byte, pos int) (advance int, ev *Event) {
	if b == '\r' {
		if d.pending {
			ev = &Event{Src: [2]int{d.pendingAt, d.pendingAt + 1}}
			d.pendingAt = pos
			return 1, ev // previous lone \r ends; new \r stays pending
		}
		d.pending, d.pendingAt = true, pos
		return 1, nil
	}
	if b == '\n' {
		if d.pending {
			ev = &Event{Src: [2]int{d.pendingAt, pos + 1}} // \r\n
			d.pending = false
			return 1, ev
		}
		return 1, &Event{Src: [2]int{pos, pos + 1}} // bare \n
	}
	if d.pending {
		ev = &Event{Src: [2]int{d.pendingAt, d.pendingAt + 1}} // lone \r
		d.pending = false
		return 0, ev
	}
	return 1, nil
}

// Flush resolves a pending \r at end of stream as a lone line ending.
func (d *Decoder) Flush() *Event {
	if d.pending {
		ev := &Event{Src: [2]int{d.pendingAt, d.pendingAt + 1}}
		d.pending = false
		return ev
	}
	return nil
}

// IsEnding reports whether b is a line-ending byte.
func IsEnding(b byte) bool { return b == '\r' || b == '\n' }
