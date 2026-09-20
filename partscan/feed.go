package partscan

import "bytes"

// Feed consumes one chunk of the stream and returns the parts that
// completed during this call. Feeding a zero-length chunk is allowed.
func (s *Scanner) Feed(p []byte) ([][]byte, error) {
	switch s.state {
	case stateFailed:
		return nil, s.err
	case stateDone:
		if len(p) > 0 {
			return nil, ErrAfterClose
		}
		return nil, nil
	}
	s.buf = append(s.buf, p...)
	if s.state == statePreamble && !s.matchPreamble() {
		return nil, s.err
	}
	if s.state != stateParts {
		return nil, nil
	}
	return s.drain(), nil
}

// matchPreamble reports whether scanning may continue. It waits for
// more bytes while buf is a proper prefix of the preamble, and fails
// terminally on the first mismatching byte.
func (s *Scanner) matchPreamble() bool {
	if len(s.buf) < len(s.preamble) {
		if bytes.HasPrefix(s.preamble, s.buf) {
			return true
		}
		s.fail(ErrNoPreamble)
		return false
	}
	if !bytes.HasPrefix(s.buf, s.preamble) {
		s.fail(ErrNoPreamble)
		return false
	}
	s.buf = s.buf[len(s.preamble):]
	s.state = stateParts
	return true
}
