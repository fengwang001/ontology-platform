// Package eol identifies line endings (\r\n, lone \r, \n) across Write cuts.
package eol

// Detector is a single-use state machine. Feed bytes one at a time with Step;
// a pending CR is reported as a boundary only when the following byte proves it
// was not part of CRLF (or at EOF via Flush).
type Detector struct {
	pendingCR bool
}

// Step feeds one byte. boundary reports that this byte completes a line ending:
// LF yields one boundary (consuming a pending CR as CRLF when present),
// CR yields a boundary only when a CR was already pending (the pair \r\r).
// The newly seen CR itself stays pending until the next byte or Flush.
func (d *Detector) Step(b byte) (boundary bool) {
	switch {
	case b == '\n':
		boundary = true // pending CR, if any, is the CRLF partner
		d.pendingCR = false
	case b == '\r':
		boundary = d.pendingCR // previous \r was a lone ending
		d.pendingCR = true
	default:
		if d.pendingCR {
			boundary = true // pending CR was a lone ending
			d.pendingCR = false
		}
	}
	return boundary
}

// Flush reports whether a trailing CR is still awaiting a decision at EOF.
// A pending CR is a line ending by itself.
func (d *Detector) Flush() (boundary bool) {
	boundary = d.pendingCR
	d.pendingCR = false
	return boundary
}

// Pending reports whether a CR is awaiting the next byte.
func (d *Detector) Pending() bool { return d.pendingCR }
