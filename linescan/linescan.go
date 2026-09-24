// Package linescan incrementally splits arbitrary byte chunks into logical
// lines. \n, \r and \r\n are equivalent line endings.
package linescan

import "errors"

// ErrLineTooLong is returned when one line exceeds MaxLineBytes.
var ErrLineTooLong = errors.New("linescan: line exceeds maximum length")

// Scanner turns byte chunks into complete lines.
type Scanner struct {
	maxLine int
	pending []byte
	cr      bool // last consumed byte was a pending \r
	bomN    int  // matched prefix length of leading UTF-8 BOM
	bomDone bool // leading BOM decision finished
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// New creates a Scanner; maxLine <= 0 means unlimited.
func New(maxLine int) *Scanner { return &Scanner{maxLine: maxLine} }

// Reset clears all buffered state.
func (s *Scanner) Reset() { s.pending, s.cr, s.bomN, s.bomDone = nil, false, 0, false }

// FlushLines emits any buffered half-line and pending CR as lines. Used only
// when the caller explicitly closes the stream.
func (s *Scanner) FlushLines() []string {
	var lines []string
	if s.cr || len(s.pending) > 0 {
		lines = append(lines, string(s.pending))
	}
	s.pending, s.cr = nil, false
	return lines
}

// State is a point-in-time snapshot of buffered scanner state.
type State struct {
	Pending []byte
	CR      bool
	BOMN    int
	BOMDone bool
}

// Snapshot captures the current state.
func (s *Scanner) Snapshot() State {
	return State{append([]byte(nil), s.pending...), s.cr, s.bomN, s.bomDone}
}

// Restore reinstates a previously captured state.
func (s *Scanner) Restore(st State) {
	s.pending = append([]byte(nil), st.Pending...)
	s.cr, s.bomN, s.bomDone = st.CR, st.BOMN, st.BOMDone
}

// Feed consumes a chunk and returns all lines completed by it. A returned
// empty line marks a blank line. Either the whole chunk is accepted or an
// error is returned with no state change.
func (s *Scanner) Feed(chunk []byte) ([]string, error) {
	snap := struct {
		pending []byte
		cr      bool
		bomN    int
		bomDone bool
	}{append([]byte(nil), s.pending...), s.cr, s.bomN, s.bomDone}
	var lines []string
	emit := func() { lines = append(lines, string(s.pending)); s.pending = nil }
	add := func(b byte) error {
		if s.maxLine > 0 && len(s.pending) >= s.maxLine {
			s.pending, s.cr, s.bomN, s.bomDone = snap.pending, snap.cr, snap.bomN, snap.bomDone
			return ErrLineTooLong
		}
		s.pending = append(s.pending, b)
		return nil
	}
	for _, b := range chunk {
		if !s.bomDone {
			if b == bom[s.bomN] {
				s.bomN++
				if s.bomN < len(bom) {
					continue
				}
				s.bomDone, s.bomN = true, 0
				continue
			}
			for _, p := range bom[:s.bomN] {
				if err := add(p); err != nil {
					return nil, err
				}
			}
			s.bomDone, s.bomN = true, 0
		}
		switch {
		case b == '\r':
			emit()
			s.cr = true // next byte decides whether this \r is a lone ending
		case b == '\n':
			if s.cr {
				s.cr = false // swallow the LF of a CRLF
			} else {
				emit()
			}
		default:
			if s.cr {
				s.cr = false
			}
			if err := add(b); err != nil {
				return nil, err
			}
		}
	}
	return lines, nil
}
