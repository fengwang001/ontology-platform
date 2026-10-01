// Package eol recognizes line endings (\r\n, lone \r, \n) in a byte
// stream, keeping a '\r' at a chunk boundary in a pending state until
// the next byte (or end of stream) decides it.
package eol

// Kind classifies a stream event.
type Kind int

const (
	// Byte is a byte that is not part of any line ending.
	Byte Kind = iota
	// Newline is one logical line ending, normalized to '\n'.
	Newline
)

// Ev is one recognized unit of the stream.
type Ev struct {
	Kind Kind
	B    byte // the byte itself, valid when Kind == Byte
	Pos  int  // offset of the event's first byte in the input stream
	Drop int  // input bytes deleted by the event (1 for the '\r' of "\r\n")
}

// Scanner is a streaming line-ending recognizer. It is not safe for
// concurrent use.
type Scanner struct {
	pend bool // a '\r' is waiting for the next byte
	pos  int  // offset of the next input byte
}

// Feed consumes one byte and reports events through out (0 to 2 calls).
func (s *Scanner) Feed(b byte, out func(Ev)) {
	if s.pend {
		s.pend = false
		if b == '\n' {
			out(Ev{Kind: Newline, Pos: s.pos - 1, Drop: 1})
			s.pos++
			return
		}
		out(Ev{Kind: Newline, Pos: s.pos - 1}) // lone '\r'
	}
	switch b {
	case '\r':
		s.pend = true
	case '\n':
		out(Ev{Kind: Newline, Pos: s.pos})
	default:
		out(Ev{Kind: Byte, B: b, Pos: s.pos})
	}
	s.pos++
}

// End flushes a trailing pending '\r' as a lone-'\r' newline.
func (s *Scanner) End(out func(Ev)) {
	if s.pend {
		s.pend = false
		out(Ev{Kind: Newline, Pos: s.pos - 1})
	}
}
