// Package eol identifies line endings (\r\n, lone \r, \n) on a byte stream,
// keeping a trailing \r pending until the next byte decides its fate.
package eol

// Kind classifies a detector event.
type Kind uint8

const (
	// Byte is an ordinary byte that is not part of any line ending.
	Byte Kind = iota
	// LF is a normalized line ending emitted at the given output byte offset.
	// For a lone \r the event offset is that \r; for \r\n it is the \n.
	LF
)

// Event describes one consumed input byte.
type Event struct {
	Kind Kind
	// Off is the input byte offset of the represented byte.
	Off int
	// Raw is the consumed input byte.
	Raw byte
	// CR is true for the \r of a \r\n pair: consumed here, produces no output.
	CR bool
}

// Detector is a single-use streaming line-ending detector.
// It is not safe for concurrent use.
type Detector struct {
	pendingCR bool
	off      int
}

// Feed consumes b and returns per-input-byte events. The returned slice is
// valid until the next call. A trailing \r remains pending until the next
// Feed byte or Flush.
func (d *Detector) Feed(b []byte) []Event {
	ev := make([]Event, 0, len(b)+1)
	for _, c := range b {
		if d.pendingCR {
			d.pendingCR = false
			if c == '\n' {
				// Previous \r is swallowed; the \n carries the line ending.
				ev = append(ev, Event{Kind: LF, Off: d.off, Raw: c})
				ev[len(ev)-2] = Event{Kind: Byte, Off: d.off - 1, Raw: '\r', CR: true}
			} else if c == '\r' {
				ev = append(ev, Event{Kind: LF, Off: d.off - 1, Raw: '\r'})
				d.pendingCR = true
			} else {
				ev = append(ev, Event{Kind: LF, Off: d.off - 1, Raw: '\r'})
				ev = append(ev, Event{Kind: Byte, Off: d.off, Raw: c})
			}
		} else if c == '\r' {
			d.pendingCR = true
			ev = append(ev, Event{Kind: Byte, Off: d.off, Raw: c})
		} else if c == '\n' {
			ev = append(ev, Event{Kind: LF, Off: d.off, Raw: c})
		} else {
			ev = append(ev, Event{Kind: Byte, Off: d.off, Raw: c})
		}
		d.off++
	}
	return ev
}

// Flush resolves a trailing pending \r as a lone-CR line ending. It returns
// the event (if any); Pending reports whether Flush has work to do.
func (d *Detector) Flush() (Event, bool) {
	if !d.pendingCR {
		return Event{}, false
	}
	d.pendingCR = false
	return Event{Kind: LF, Off: d.off - 1, Raw: '\r'}, true
}

// Pending reports whether a \r awaits the next byte.
func (d *Detector) Pending() bool { return d.pendingCR }

// Offset reports the number of input bytes consumed so far.
func (d *Detector) Offset() int { return d.off }
