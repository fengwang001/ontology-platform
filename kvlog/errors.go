package kvlog

import "errors"

// Kind classifies engine errors. When several error conditions apply at once
// the one with the smallest Kind value is reported.
type Kind int

const (
	KindInvalidArgument Kind = iota + 1
	KindSegmentCorrupt
	KindKeyDirDistorted
	KindSegmentNotFound
	KindActiveSegmentNotMergeable
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid argument"
	case KindSegmentCorrupt:
		return "segment corrupt"
	case KindKeyDirDistorted:
		return "keydir distorted"
	case KindSegmentNotFound:
		return "segment not found"
	case KindActiveSegmentNotMergeable:
		return "active segment not mergeable"
	default:
		return "unknown error"
	}
}

// Error is the structured error type returned by the engine.
type Error struct {
	Kind    Kind
	Segment int // segment id when applicable, -1 otherwise
	Offset  int // first corrupt offset within the segment when applicable
	Op      string
	Msg     string
}

func (e *Error) Error() string {
	s := "kvlog: " + e.Kind.String()
	if e.Op != "" {
		s += " [" + e.Op + "]"
	}
	if e.Segment >= 0 {
		s += " segment=" + itoa(e.Segment)
	}
	if e.Offset >= 0 {
		s += " offset=" + itoa(e.Offset)
	}
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	return s
}

func newError(k Kind, op string, seg, off int, msg string) *Error {
	return &Error{Kind: k, Op: op, Segment: seg, Offset: off, Msg: msg}
}

// AsError extracts *Error from err when present.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
