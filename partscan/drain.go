package partscan

import "bytes"

// drain scans buffered bytes for delimiters and returns every part that
// completes. It keeps at most len(marker)+1 undecided bytes in buf, so
// memory stays bounded regardless of chunking.
func (s *Scanner) drain() [][]byte {
	var out [][]byte
	for s.state == stateParts {
		i := bytes.Index(s.buf, s.marker)
		if i < 0 {
			s.holdPartialMarker()
			return out
		}
		rest := s.buf[i+len(s.marker):]
		if len(rest) < 2 {
			// Not enough bytes to classify the marker yet.
			s.part = append(s.part, s.buf[:i]...)
			s.buf = append(s.buf[:0], s.buf[i:]...)
			return out
		}
		switch {
		case rest[0] == '\r' && rest[1] == '\n':
			out = append(out, s.emit(i))
			s.buf = s.buf[i+len(s.marker)+2:]
		case rest[0] == '-' && rest[1] == '-':
			out = append(out, s.emit(i))
			s.state = stateDone
			s.buf = nil // trailing bytes after the close delimiter are ignored
		default:
			// False alarm: the marker bytes are part content. Advance
			// one byte so an overlapping real marker is not missed.
			s.part = append(s.part, s.buf[:i+1]...)
			s.buf = s.buf[i+1:]
		}
	}
	return out
}

// holdPartialMarker moves content that cannot start a marker into part,
// keeping only the longest suffix of buf that is a prefix of marker.
func (s *Scanner) holdPartialMarker() {
	keep := suffixPrefixOverlap(s.buf, s.marker)
	s.part = append(s.part, s.buf[:len(s.buf)-keep]...)
	s.buf = append(s.buf[:0], s.buf[len(s.buf)-keep:]...)
}

// emit returns the completed part (content plus the n bytes preceding
// the delimiter) as a freshly allocated slice, never nil.
func (s *Scanner) emit(n int) []byte {
	out := make([]byte, len(s.part)+n)
	copy(out, s.part)
	copy(out[len(s.part):], s.buf[:n])
	s.part = nil
	return out
}

// suffixPrefixOverlap returns the length of the longest suffix of s
// (shorter than sep) that is also a prefix of sep.
func suffixPrefixOverlap(s, sep []byte) int {
	max := len(sep) - 1
	if len(s) < max {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if bytes.HasPrefix(sep, s[len(s)-k:]) {
			return k
		}
	}
	return 0
}
