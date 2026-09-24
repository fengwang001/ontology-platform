// Package eol identifies line endings across streaming chunk boundaries.
package eol

// Kind classifies a decoded event.
type Kind uint8

const (
	Byte  Kind = iota // an ordinary byte
	Break             // one normalized line ending
)

// Event is one decoded item. Start/End are original-byte offsets [Start,End).
// A Byte spans exactly one byte; a Break spans one byte (\r or \n) or two (\r\n).
type Event struct {
	Kind       Kind
	Start, End int
}

// Decoder is a one-pass, non-concurrency-safe streaming line-ending decoder.
// It recognizes \r\n, lone \r and \n. A trailing \r stays pending until the
// next byte or Flush resolves it, so behavior is independent of chunk cuts.
type Decoder struct {
	pendingCR bool
	crOff     int
}

// Push feeds one byte at absolute original offset off and returns zero to two
// events (a pending \r may resolve together with the new byte).
func (d *Decoder) Push(b byte, off int) []Event {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return []Event{{Kind: Break, Start: d.crOff, End: off + 1}}
		}
		ev := []Event{{Kind: Break, Start: d.crOff, End: d.crOff + 1}}
		if b == '\r' {
			d.pendingCR = true
			d.crOff = off
		} else {
			ev = append(ev, Event{Kind: Byte, Start: off, End: off + 1})
		}
		return ev
	}
	if b == '\r' {
		d.pendingCR = true
		d.crOff = off
		return nil
	}
	if b == '\n' {
		return []Event{{Kind: Break, Start: off, End: off + 1}}
	}
	return []Event{{Kind: Byte, Start: off, End: off + 1}}
}

// Flush resolves a trailing pending \r as a lone line ending. It must be
// called once after the final byte.
func (d *Decoder) Flush() (Event, bool) {
	if d.pendingCR {
		d.pendingCR = false
		return Event{Kind: Break, Start: d.crOff, End: d.crOff + 1}, true
	}
	return Event{}, false
}

// PendingCR reports whether a \r is awaiting the next byte and returns its offset.
func (d *Decoder) PendingCR() (int, bool) { return d.crOff, d.pendingCR }

// Reset returns the decoder to its initial state.
func (d *Decoder) Reset() { *d = Decoder{} }
