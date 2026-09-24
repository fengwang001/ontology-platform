// Package eol recognizes mixed line endings (\r\n, lone \r, \n) in a byte
// stream. A trailing \r stays pending until the next byte decides whether it
// starts a \r\n pair or is a lone line ending.
package eol

// Action is what the caller must do with the byte fed to Decoder.Feed.
type Action uint8

const (
	// Pass means the byte is emitted unchanged at its current position.
	Pass Action = iota
	// DeleteCR means the \r byte is dropped; the following byte (already fed
	// or the synthetic event from Flush) carries the emitted \n.
	DeleteCR
	// LoneLF means the \r byte becomes a lone line ending emitted as \n.
	LoneLF
	// EmitLF means the fed byte is \n that closes a \r\n pair: emit one \n.
	EmitLF
)

// Decoder is a single state bit: whether the previous byte is a pending \r.
// It is not safe for concurrent use.
type Decoder struct {
	pendingCR bool
}

// NewDecoder returns a decoder with no pending carriage return.
func NewDecoder() *Decoder { return &Decoder{} }

// Feed consumes one byte b and reports one or two actions. The first return
// value is always meaningful. When a pending \r resolves as a lone line
// ending (the new byte is not \n), first is LoneLF and second is the action
// for the newly fed byte itself. Otherwise second is Pass (ignore it).
func (d *Decoder) Feed(b byte) (Action, Action) {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return EmitLF, Pass
		}
		return LoneLF, d.feedFresh(b)
	}
	return d.feedFresh(b), Pass
}

// feedFresh classifies a byte with no pending carriage return.
func (d *Decoder) feedFresh(b byte) Action {
	if b == '\r' {
		d.pendingCR = true
		return DeleteCR
	}
	if b == '\n' {
		return EmitLF
	}
	return Pass
}

// Pending reports whether a \r is awaiting the next byte.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Flush resolves end of stream. It returns true (and clears the pending
// state) when a trailing \r is a lone line ending that must be emitted as \n.
func (d *Decoder) Flush() bool {
	if d.pendingCR {
		d.pendingCR = false
		return true
	}
	return false
}

// Reset returns the decoder to its initial state.
func (d *Decoder) Reset() { d.pendingCR = false }
