// Package eol recognizes mixed line endings: "\r\n", lone "\r", and "\n".
// A '\r' is held pending (no output yet) until the next byte resolves it, so
// the normalized '\n' can be mapped directly to its final original position.
package eol

// Event reports how one input byte was resolved.
type Event struct {
	// LFAt is >= 0 when a normalized '\n' must be emitted: it equals the
	// original offset that '\n' maps to (the held '\r' offset, or the
	// current '\n' offset for a "\r\n" pair or a lone '\n').
	LFAt int64
	// HeldCR means the current byte '\r' entered pending state.
	HeldCR bool
	// Consumed means the current byte was fully used (the '\n' of "\r\n")
	// and the caller must not process it further. Otherwise the caller
	// still runs whitespace logic on the current byte.
	Consumed bool
}

// Detector is a single-byte-step automaton; not safe for concurrent use.
type Detector struct {
	pending bool
	crPos   int64
}

// Feed resolves byte b at original offset pos.
func (d *Detector) Feed(b byte, pos int64) Event {
	if d.pending {
		p := d.crPos
		if b == '\n' {
			d.pending = false
			return Event{LFAt: pos, Consumed: true}
		}
		d.pending = false
		ev := Event{LFAt: p}
		if b == '\r' {
			d.pending = true
			d.crPos = pos
			ev.HeldCR = true
		}
		return ev
	}
	if b == '\r' {
		d.pending = true
		d.crPos = pos
		return Event{HeldCR: true}
	}
	if b == '\n' {
		return Event{LFAt: pos}
	}
	return Event{}
}

// Flush resolves a pending '\r' at Close: it was a lone line ending. Returns
// its original offset, or -1 if nothing was pending.
func (d *Detector) Flush() int64 {
	if !d.pending {
		return -1
	}
	p := d.crPos
	d.pending = false
	return p
}

// Pending reports whether a '\r' awaits resolution.
func (d *Detector) Pending() bool { return d.pending }

// CRPos reports the held '\r' original offset (valid when Pending).
func (d *Detector) CRPos() int64 { return d.crPos }

// Reset clears all state.
func (d *Detector) Reset() { d.pending = false }
