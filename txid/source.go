package txid

import "sync"

// Source hands out strictly monotonic transaction identifiers.
//
// The source itself is the only origin of transaction ids: the store never
// invokes time.Now or invents ids. Advance lets the same logical stream be
// resumed after a simulated crash/restart with an in-memory WAL replay.
type Source struct {
	mu     sync.Mutex
	last   TxID
	exhausted bool
}

// Next returns the next identifier, which is always strictly greater than
// every identifier previously issued by this source. It returns ErrExhausted
// if issuing one more identifier would wrap around to zero.
func (s *Source) Next() (TxID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exhausted || s.last == ^TxID(0) {
		s.exhausted = true
		return 0, ErrExhausted
	}
	s.last++
	return s.last, nil
}

// Last returns the greatest identifier issued so far (zero before any Next).
func (s *Source) Last() TxID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Advance moves the stream's high-water mark to at least want. It is used
// after WAL replay so that reused transaction ids never happen. Advancing
// never decreases the stream.
func (s *Source) Advance(want TxID) error {
	if want == 0 {
		return ErrIllegal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if want == ^TxID(0) {
		s.exhausted = true
	}
	if uint64(want) > uint64(s.last) {
		s.last = want
	}
	return nil
}
