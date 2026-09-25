package ws

type State struct {
	buf []byte
}

func IsTrailing(b byte) bool { return b == ' ' || b == '\t' }

func (s *State) Len() int { return len(s.buf) }

func (s *State) Bytes() []byte { return s.buf }

func (s *State) Add(b byte) { s.buf = append(s.buf, b) }

// Take removes and returns the buffered run; it must only be called once the
// run is known to be ordinary internal whitespace.
func (s *State) Take() []byte {
	out := s.buf
	s.buf = nil
	return out
}

// Drop removes a run known to be trailing whitespace.
func (s *State) Drop() []byte {
	out := s.buf
	s.buf = nil
	return out
}
