package partscan

// Feed delivers one chunk of the stream (possibly empty) and returns the
// parts completed by this chunk. Returned slices are independent copies;
// mutating them, or mutating p after the call, never affects the scanner.
func (s *Scanner) Feed(p []byte) ([][]byte, error) {
	switch s.phase {
	case phaseError:
		return nil, s.err
	case phaseDone:
		if len(p) > 0 {
			s.phase = phaseError
			s.err = ErrAfterClose
			return nil, ErrAfterClose
		}
		return nil, nil
	}

	// Append a private copy so later caller mutations cannot leak in.
	if len(p) > 0 {
		s.buf = append(s.buf, p...)
	}

	var out [][]byte
	var err error
	if s.phase == phasePreamble {
		out, err = s.scanPreamble(out)
		if err != nil {
			return out, err
		}
	}

	if s.phase == phasePart {
		out, err = s.scanPart(out)
	}
	return out, err
}

// fail enters the terminal error state. Any buffered part content is dropped.
func (s *Scanner) fail(err error) ([][]byte, error) {
	s.phase = phaseError
	s.err = err
	s.buf = nil
	s.pos = 0
	s.match.reset()
	return nil, err
}
