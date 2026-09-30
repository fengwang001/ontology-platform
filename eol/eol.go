// Package eol recognizes the three line-ending shapes across stream cuts.
//
// The only cross-call state is a trailing CR whose partner LF has not arrived
// yet; Feed reports both the resolution of that pending CR and the role of the
// current byte, so callers never lose or duplicate a boundary at a cut point.
package eol

const (
	CR byte = '\r'
	LF byte = '\n'
)

func IsCR(b byte) bool { return b == CR }
func IsLF(b byte) bool { return b == LF }

// Kind classifies the byte handed to Feed.
type Kind int

const (
	// KindNone: the byte is ordinary content (a held CR is not reported).
	KindNone Kind = iota
	// KindLF: the byte is an LF (it may complete a pending CRLF).
	KindLF
	// KindCR: the byte is a CR already known to be lone (previous byte was CR).
	KindCR
)

// State remembers at most one pending CR.
type State struct {
	pending bool
}

func New() *State { return &State{} }

// Pending reports whether the last seen byte is a CR awaiting the next byte.
func (s *State) Pending() bool { return s.pending }

// Feed consumes one input byte. lonePrev means the previously held CR is now
// known to be a lone line ending and must be emitted before processing this
// byte. kind describes the current byte.
func (s *State) Feed(b byte) (lonePrev bool, kind Kind) {
	if s.pending {
		s.pending = false
		switch {
		case IsLF(b):
			return false, KindLF
		case IsCR(b):
			s.pending = true
			return true, KindCR
		default:
			return true, KindNone
		}
	}
	if IsCR(b) {
		s.pending = true
		return false, KindNone
	}
	if IsLF(b) {
		return false, KindLF
	}
	return false, KindNone
}

// Flush resolves a held CR at end of stream: lone is true when one is pending.
func (s *State) Flush() (lone bool) {
	lone = s.pending
	s.pending = false
	return lone
}
