// Package eol recognizes line endings (\r\n, lone \r, \n) across Write calls.
package eol

// Event is one lexical item emitted by Splitter.
type Event struct {
	Byte byte // the original byte when Kind == Lit
	Kind Kind // Lit: raw byte; NL: one logical line ending
	Orig int  // original start offset of this event
	Len  int  // original byte length (NL is 1 for lone \r/\n, 2 for \r\n)
}

// Kind classifies an event.
type Kind uint8

const (
	Lit Kind = iota // ordinary byte
	NL              // a line ending, always rendered as one byte ('\n')
)

// Splitter is a stateful, non-concurrent scanner. Feed arbitrary byte chunks;
// a trailing \r stays pending until the next byte proves it \r\n or lone \r.
type Splitter struct {
	pendingCR bool
	pos       int // number of original bytes already consumed (excluding pending)
}

// Feed consumes b and appends events to out. A trailing \r is held back.
func (s *Splitter) Feed(b []byte, out []Event) []Event {
	for len(b) > 0 {
		if s.pendingCR {
			s.pendingCR = false
			if b[0] == '\n' {
				out = append(out, Event{Kind: NL, Byte: '\n', Orig: s.pos - 1, Len: 2})
				b, s.pos = b[1:], s.pos+1
				continue
			}
			out = append(out, Event{Kind: NL, Byte: '\n', Orig: s.pos - 1, Len: 1})
			continue // reprocess b[0] without consuming
		}
		switch b[0] {
		case '\r':
			s.pendingCR, s.pos = true, s.pos+1
			b = b[1:]
		case '\n':
			out = append(out, Event{Kind: NL, Byte: '\n', Orig: s.pos, Len: 1})
			b, s.pos = b[1:], s.pos+1
		default:
			out = append(out, Event{Kind: Lit, Byte: b[0], Orig: s.pos, Len: 1})
			b, s.pos = b[1:], s.pos+1
		}
	}
	return out
}

// Flush resolves a pending \r as a lone line ending at stream end.
func (s *Splitter) Flush(out []Event) []Event {
	if s.pendingCR {
		s.pendingCR = false
		out = append(out, Event{Kind: NL, Byte: '\n', Orig: s.pos - 1, Len: 1})
	}
	return out
}

// FlushRaw resolves a pending \r at stream end as a raw literal (fragment mode).
func (s *Splitter) FlushRaw(out []Event) []Event {
	if s.pendingCR {
		s.pendingCR = false
		out = append(out, Event{Kind: Lit, Byte: '\r', Orig: s.pos - 1, Len: 1})
	}
	return out
}

// Pending reports whether a \r is awaiting the next byte.
func (s *Splitter) Pending() bool { return s.pendingCR }

// Pos reports original bytes consumed including a pending \r.
func (s *Splitter) Pos() int {
	if s.pendingCR {
		return s.pos
	}
	return s.pos
}
