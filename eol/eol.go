// Package eol recognizes the three line-ending shapes, including a lone CR
// whose fate is undecided until the next byte arrives.
package eol

// Kind classifies a byte relative to line endings.
type Kind uint8

const (
	Other Kind = iota
	CR             // '\r'
	LF             // '\n'
)

// Classify reports whether b is CR, LF, or an ordinary byte.
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// Split is a streaming splitter. When it holds a pending CR and the next byte
// is LF, the pair is a single CRLF ending; otherwise the CR is a lone ending.
type Split struct {
	pending bool // a CR awaiting the following byte
}

// Event is one recognition result.
type Event struct {
	CRLF  bool // a complete "\r\n" ending
	Lone  bool // a lone CR ending (not followed by LF)
	LF    bool // a lone LF ending
	Other bool // an ordinary byte (value supplied by caller)
}

// New returns an empty splitter.
func New() *Split { return &Split{} }

// Pending reports whether a CR is currently held undecided.
func (s *Split) Pending() bool { return s.pending }

// Feed classifies b. When a pending CR exists it returns the event for the CR
// first (Lone unless b is LF, in which case CRLF consumes both) and, for CRLF,
// the caller must not re-feed the LF.
func (s *Split) Feed(b byte) (first Event, consumed bool) {
	if s.pending {
		s.pending = false
		if b == '\n' {
			return Event{CRLF: true}, true
		}
		return Event{Lone: true}, false
	}
	switch b {
	case '\r':
		s.pending = true
		return Event{}, true
	case '\n':
		return Event{LF: true}, true
	default:
		return Event{Other: true}, true
	}
}

// Flush resolves a pending CR at end of stream as a lone ending.
func (s *Split) Flush() (Event, bool) {
	if s.pending {
		s.pending = false
		return Event{Lone: true}, true
	}
	return Event{}, false
}
