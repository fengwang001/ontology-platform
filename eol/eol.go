// Package eol recognizes line endings (\r\n, lone \r, \n) across chunk
// boundaries. A trailing \r stays pending until the next byte decides.
package eol

// Kind classifies an input byte relative to line endings.
type Kind uint8

const (
	Regular Kind = iota // ordinary byte, emitted unchanged
	LF                  // a \n that closes a line
	CR                  // a \r whose following byte is still unknown
)

// Classify reports the raw kind of a byte.
func Classify(b byte) Kind {
	switch b {
	case '\n':
		return LF
	case '\r':
		return CR
	default:
		return Regular
	}
}

// Event is one resolved output step: either one kept byte or one normalized
// newline. OrigLen is the number of input bytes consumed (2 for \r\n).
type Event struct {
	Byte    byte
	NewLine bool
	OrigLen int
}

// Decider is a small state machine: feed input chunks in order, receive
// resolved events. Pending \r is resolved as a lone CR line ending on Flush.
type Decider struct {
	pendingCR bool
}

// New returns an empty Decider.
func New() *Decider { return &Decider{} }

// Push feeds one chunk and appends resolved events to out.
func (d *Decider) Push(p []byte, out []Event) []Event {
	for _, b := range p {
		if d.pendingCR {
			d.pendingCR = false
			out = append(out, Event{Byte: '\n', NewLine: true, OrigLen: 1})
			if b == '\n' {
				out[len(out)-1].OrigLen = 2
				continue
			}
			if b == '\r' {
				d.pendingCR = true
				continue
			}
			out = append(out, Event{Byte: b, OrigLen: 1})
			continue
		}
		if b == '\r' {
			d.pendingCR = true
			continue
		}
		if b == '\n' {
			out = append(out, Event{Byte: '\n', NewLine: true, OrigLen: 1})
			continue
		}
		out = append(out, Event{Byte: b, OrigLen: 1})
	}
	return out
}

// Flush resolves any pending \r as a lone-CR line ending.
func (d *Decider) Flush(out []Event) []Event {
	if d.pendingCR {
		d.pendingCR = false
		out = append(out, Event{Byte: '\n', NewLine: true, OrigLen: 1})
	}
	return out
}
