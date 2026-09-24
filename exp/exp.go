// Package exp implements consistent-cursor export sessions over wlog.
package exp

import (
	"errors"
	"sync"

	"ontology/wlog"
)

var (
	// ErrNotStarted is returned when Next/Finish precede Start.
	ErrNotStarted = errors.New("exp: session not started")
	// ErrFinished is returned when using a session after Finish.
	ErrFinished = errors.New("exp: session already finished")
)

// Session is one export. Snapshot segment: seqs (0,S]; incremental: (S,lastEmitted].
type Session struct {
	mu          sync.Mutex
	log         *wlog.Log
	onFinish    func()
	s           int
	lastEmitted int
	checked     int // non-exported: entries inspected by the last Next
	started     bool
	finished    bool
}

// NewSession binds a session to a log; onFinish runs once at Finish.
func NewSession(log *wlog.Log, onFinish func()) *Session {
	return &Session{log: log, onFinish: onFinish}
}

// Start records the cursor S. Fails if already started or finished.
func (s *Session) Start(S int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return ErrFinished
	}
	if s.started {
		return errors.New("exp: session already started")
	}
	s.started = true
	s.s = S
	return nil
}

// Next emits exactly lastEmitted+1 when it exists, never skipping ahead.
// ok=false (nil error) means the next entry does not exist yet.
func (s *Session) Next() (wlog.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return wlog.Entry{}, false, ErrNotStarted
	}
	if s.finished {
		return wlog.Entry{}, false, ErrFinished
	}
	next := s.lastEmitted + 1
	e, ok := s.log.Entry(next) // direct locate by Seq: O(1) checks
	s.checked = 1
	if !ok {
		return wlog.Entry{}, false, nil
	}
	s.lastEmitted = next
	return e, true, nil
}

// Finish ends the session, reporting [1,S] and [S+1,lastEmitted], and
// verifies the emitted sequence is exactly 1..lastEmitted with no gaps.
func (s *Session) Finish() (snapHi, incLo, incHi int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return 0, 0, 0, ErrNotStarted
	}
	if s.finished {
		return 0, 0, 0, ErrFinished
	}
	for i := 1; i <= s.lastEmitted; i++ {
		if _, ok := s.log.Entry(i); !ok {
			return 0, 0, 0, errors.New("exp: gap in emitted sequence")
		}
	}
	s.finished = true
	if s.onFinish != nil {
		s.onFinish()
	}
	return s.s, s.s + 1, s.lastEmitted, nil
}
