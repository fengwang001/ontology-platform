// Package eol classifies byte stream line endings: CRLF, lone CR, LF,
// and a CR pending at a split point. It has no dependencies.
package eol

// Action describes how one input byte is translated.
type Action uint8

const (
	// Data: byte passes through unchanged.
	Data Action = iota
	// LF: byte (or pair) emits one normalized '\n'.
	LF

	// Pending: CR awaiting the next byte; resolved by the next Feed or Flush.
	Pending
)

// Decoder is a one-byte state machine. CR alone is a line ending; CR
// followed by LF is one line ending; CR followed by CR is two endings.
type Decoder struct {
	pending bool
}

// New returns a fresh Decoder.
func New() *Decoder { return &Decoder{} }

// Feed feeds one byte and returns the actions it produces, in order.
// When the previous byte was a pending CR, up to two actions are
// returned (the resolution of the old CR plus the action for b).
func (d *Decoder) Feed(b byte) []Action {
	if d.pending {
		d.pending = false
		switch {
		case b == '\n':
			return []Action{LF} // "\r\n": CR folded into the LF
		case b == '\r':
			d.pending = true
			return []Action{LF} // "\r\r": first CR is its own ending
		default:
			return []Action{LF, Data}
		}
	}
	switch b {
	case '\r':
		d.pending = true
		return []Action{Pending}
	case '\n':
		return []Action{LF}
	default:
		return []Action{Data}
	}
}

// Flush resolves state at end of stream. A dangling CR is a lone line
// ending (LF); the return value is meaningful only when Pending was true.
func (d *Decoder) Flush() Action {
	if d.pending {
		d.pending = false
		return LF
	}
	return Data
}

// Pending reports whether a CR is currently unresolved.
func (d *Decoder) Pending() bool { return d.pending }
