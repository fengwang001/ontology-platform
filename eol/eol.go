// Package eol recognizes the three line endings "\r\n", "\r" and "\n"
// across arbitrary byte-chunk boundaries.
package eol

// Kind classifies one fed byte.
type Kind uint8

const (
	Other   Kind = iota // ordinary byte
	LF                  // a line ending has just been emitted (see EolLen)
	Pending             // \r buffered; the next byte decides CRLF vs lone CR
)

// Result is the outcome of feeding one byte.
type Result struct {
	Kind     Kind
	Replay   byte // valid when ReplayOK: byte that must be fed again
	ReplayOK bool
	EolLen   int // when Kind==LF: 1 for lone \r or \n, 2 for \r\n
}

// Decoder is a single-user state machine, zero value ready.
type Decoder struct {
	cr bool
}

// Reset returns the decoder to its initial state.
func (d *Decoder) Reset() { d.cr = false }

// Pending reports whether a trailing \r is currently undecided.
func (d *Decoder) Pending() bool { return d.cr }

// Feed consumes one byte. When a pending \r is resolved as a lone CR the
// current byte is returned in Replay and must be fed again after the caller
// has processed the lone-CR line ending.
func (d *Decoder) Feed(b byte) Result {
	if d.cr {
		d.cr = false
		if b == '\n' {
			return Result{Kind: LF, EolLen: 2}
		}
		return Result{Kind: LF, EolLen: 1, Replay: b, ReplayOK: true}
	}
	switch b {
	case '\r':
		d.cr = true
		return Result{Kind: Pending}
	case '\n':
		return Result{Kind: LF, EolLen: 1}
	default:
		return Result{Kind: Other}
	}
}

// Flush resolves a pending \r at end of stream as a lone-CR line ending.
func (d *Decoder) Flush() bool {
	if d.cr {
		d.cr = false
		return true
	}
	return false
}
