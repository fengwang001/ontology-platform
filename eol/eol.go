// Package eol recognizes the three line-ending forms (\r\n, lone \r, \n)
// across Write boundaries, including a \r whose fate is undecided until the
// next byte arrives.
package eol

// Event classifies a fed byte.
type Event uint8

const (
	// Content is a byte that is not part of a line ending.
	Content Event = iota
	// LF is the single normalized '\n' of a completed line ending.
	LF
	// PendingCR is a '\r' that may still be followed by '\n'.
	PendingCR
)

// Splitter is a one-byte-at-a-time streaming classifier.
type Splitter struct {
	pending bool
}

// Feed consumes byte b. When the previous byte was a pending '\r' and b is not
// '\n', the lone '\r' is finalized first: the returned events are [LF, ev].
func (s *Splitter) Feed(b byte) []Event {
	switch {
	case s.pending && b == '\n':
		s.pending = false
		return []Event{LF} // \r\n: the \r was eaten, the \n becomes one LF
	case s.pending:
		s.pending = false
		if b == '\r' {
			s.pending = true
			return []Event{LF, PendingCR} // \r\r : first line ends, new pending
		}
		return []Event{LF, Content}
	case b == '\r':
		s.pending = true
		return []Event{PendingCR}
	case b == '\n':
		return []Event{LF}
	default:
		return []Event{Content}
	}
}

// Flush finalizes a trailing pending '\r' as a lone-CR line ending.
func (s *Splitter) Flush() []Event {
	if !s.pending {
		return nil
	}
	s.pending = false
	return []Event{LF}
}

// Force resolves a pending '\r' as a lone-CR line ending without consuming the
// next byte (used at segment boundaries where the following byte belongs to
// another segment).
func (s *Splitter) Force() []Event { return s.Flush() }

// Pending reports whether a '\r' is awaiting its successor.
func (s *Splitter) Pending() bool { return s.pending }
