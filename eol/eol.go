// Package eol recognizes mixed line endings one byte at a time.
package eol

const (
	None byte = iota
	CR
	LF
	CRLF
)

// Event reports a byte ending at offset in the original stream.
type Event struct {
	Byte   byte
	Kind   byte
	Offset int
}

// Scanner keeps only the unresolved trailing CR state.
type Scanner struct {
	pendingCR bool
	offset    int
}

// New returns a scanner positioned before the stream.
func New() *Scanner { return &Scanner{} }

// Feed processes b. Events are emitted in original byte order; a CR event may
// be emitted one byte late because the following LF can turn CRLF into one end.
func (s *Scanner) Feed(b []byte) []Event {
	events := make([]Event, 0, len(b)+1)
	for _, c := range b {
		off := s.offset
		s.offset++
		switch c {
		case '\r':
			if s.pendingCR {
				events = append(events, Event{Byte: '\r', Kind: CR, Offset: off - 1})
			}
			s.pendingCR = true
		case '\n':
			if s.pendingCR {
				events = append(events, Event{Byte: '\n', Kind: CRLF, Offset: off})
				s.pendingCR = false
			} else {
				events = append(events, Event{Byte: '\n', Kind: LF, Offset: off})
			}
		default:
			if s.pendingCR {
				events = append(events, Event{Byte: '\r', Kind: CR, Offset: off - 1})
				s.pendingCR = false
			}
			events = append(events, Event{Byte: c, Kind: None, Offset: off})
		}
	}
	return events
}

// Close emits a CR not followed by LF.
func (s *Scanner) Close() []Event {
	if !s.pendingCR {
		return nil
	}
	s.pendingCR = false
	return []Event{{Byte: '\r', Kind: CR, Offset: s.offset - 1}}
}

// Offset reports the number of bytes consumed.
func (s *Scanner) Offset() int { return s.offset }
