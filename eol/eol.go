// Package eol recognizes the three streamed line-ending forms.
package eol

// Event describes bytes emitted by Feed.
type Event uint8

const (
	None Event = iota
	Text
	LineEnd
	PendingCR
)

// Detector resolves a trailing CR only after seeing the following byte.
type Detector struct {
	pending bool
}

// Feed consumes b and returns one event together with its byte length.
// A trailing CR yields PendingCR until Close resolves it.
func (d *Detector) Feed(b byte) (Event, int) {
	if d.pending {
		d.pending = false
		if b == '\n' {
			return LineEnd, 2
		}
		return Text, 0
	}
	switch {
	case b == '\n':
		return LineEnd, 1
	case b == '\r':
		d.pending = true
		return PendingCR, 1
	default:
		return Text, 1
	}
}

// ResolvePending reports whether a pending CR must be emitted before b.
func (d *Detector) ResolvePending(b byte) bool {
	if !d.pending {
		return false
	}
	d.pending = false
	if b == '\n' {
		return false
	}
	return true
}

// Close resolves a dangling CR as its own line end.
func (d *Detector) Close() bool {
	if !d.pending {
		return false
	}
	d.pending = false
	return true
}

// Pending reports whether the detector currently holds one CR.
func (d *Detector) Pending() bool { return d.pending }

// Reset removes all detector state.
func (d *Detector) Reset() { d.pending = false }
