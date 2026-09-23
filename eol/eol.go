// Package eol identifies mixed line endings (\r\n, lone \r, \n), including
// the undecided state of a trailing \r at a stream split point.
package eol

// Event describes how a fed byte participates in a line ending.
type Event uint8

const (
	// Content means the byte is not part of a line ending.
	Content Event = iota
	// LFCR is an '\n' emitted as the canonical line ending (lone \n or \r\n).
	LF
	// CR is a lone '\r' that forms its own line ending (e.g. the first \r in \r\r\n).
	CR
	// PendingCR is a '\r' whose following byte is not yet known.
	PendingCR
	// ResumePending is the byte following a pending \r when it was not '\n';
	// that byte is delivered separately as Content.
	ResumePending
)

// Detector is a single-byte state machine. It is not safe for concurrent use.
type Detector struct {
	pendingCR bool
}

// Feed consumes one byte and returns the event for it. A '\n' immediately
// after a pending '\r' completes a CRLF and yields LF; otherwise a pending
// '\r' resolves to its own line ending (CR) before the new byte is processed.
func (d *Detector) Feed(b byte) Event {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return LF
		}
		if b == '\r' {
			d.pendingCR = true
			return CR
		}
		return ResumePending
	}
	switch b {
	case '\n':
		return LF
	case '\r':
		d.pendingCR = true
		return PendingCR
	default:
		return Content
	}
}

// Flush resolves a trailing pending '\r' at end of stream as its own line
// ending. It returns true (and clears the pending state) when an ending was
// emitted.
func (d *Detector) Flush() bool {
	if d.pendingCR {
		d.pendingCR = false
		return true
	}
	return false
}

// Pending reports whether a '\r' awaits the next byte.
func (d *Detector) Pending() bool { return d.pendingCR }
