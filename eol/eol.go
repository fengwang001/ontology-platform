// Package eol recognizes line endings across write boundaries:
// "\r\n", lone "\r", and "\n". A trailing "\r" stays pending until the
// next byte or Close decides whether it pairs with a following "\n".
package eol

// Event classifies the result of feeding one byte.
type Event uint8

const (
	Content Event = iota // byte is ordinary content, consume it
	NL                   // a complete line ending ended here
	Flush                // pending "\r" resolved as lone CR: caller emits NL then refeeds byte
)

// Decoder is a tiny streaming state machine. It is not safe for
// concurrent use.
type Decoder struct {
	pending bool
	pendingAt int
}

// NewDecoder returns a decoder in the empty state.
func NewDecoder() *Decoder { return &Decoder{} }

// Feed supplies byte b at original input offset pos.
func (d *Decoder) Feed(b byte, pos int) Event {
	if d.pending {
		d.pending = false
		if b == '\n' {
			return NL // "\r\n": pair consumed, newline started at pendingAt
		}
		d.pendingAt = pos // refeed b after flushing
		return Flush
	}
	if b == '\r' {
		d.pending = true
		d.pendingAt = pos
		return Content
	}
	if b == '\n' {
		d.pendingAt = pos
		return NL
	}
	return Content
}

// RefeedPos reports the original offset of the byte carried with a
// Flush event that must be fed again.
func (d *Decoder) RefeedPos() int { return d.pendingAt }

// Pending reports whether a "\r" awaits resolution and its offset.
func (d *Decoder) Pending() (bool, int) { return d.pending, d.pendingAt }

// Close resolves end of stream: a pending "\r" is a lone line ending.
// It returns true when the caller must emit one trailing newline.
func (d *Decoder) Close() bool {
	if d.pending {
		d.pending = false
		return true
	}
	return false
}
