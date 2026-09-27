// Package eol identifies line endings (\r\n, lone \r, \n) across stream cuts.
package eol

// Event classifies one byte delivered by Feed.
type Event uint8

const (
	Data     Event = iota // ordinary byte
	LFEOL                 // byte is the \n of a \r\n or lone \n pair
	CREOL                 // lone \r, emitted as a synthetic line end
	Pending               // \r awaits the next byte to decide CRLF vs lone CR
)

// Detector is a streaming line-ending classifier. Zero value is ready.
type Detector struct {
	pending bool
}

// Feed hands one byte to the detector. pendingOut (when Event is Pending)
// signals that a previously buffered \r is being resolved in the same call.
func (d *Detector) Feed(b byte) (ev Event, pendingOut bool) {
	if d.pending {
		d.pending = false
		if b == '\n' {
			return LFEOL, true
		}
		if b == '\r' {
			d.pending = true
			return CREOL, true
		}
		return Data, true
	}
	if b == '\r' {
		d.pending = true
		return Pending, false
	}
	if b == '\n' {
		return LFEOL, false
	}
	return Data, false
}

// Flush resolves a pending \r at stream end as a lone CR line ending.
func (d *Detector) Flush() (eol bool) {
	if d.pending {
		d.pending = false
		return true
	}
	return false
}

// Pending reports whether a \r awaits resolution.
func (d *Detector) Pending() bool { return d.pending }

// Reset clears detector state.
func (d *Detector) Reset() { d.pending = false }
