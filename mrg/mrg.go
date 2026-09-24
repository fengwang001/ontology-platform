// Package mrg manages many sessions keyed by Sid and dispatches
// Append/Close with idempotence and conflict judgement.
package mrg

import (
	"errors"
	"sync"

	"ontology/seg"
)

// ErrEmptySid rejects events with an empty session key.
var ErrEmptySid = errors.New("mrg: sid must not be empty")

// Merger routes events to per-Sid sessions. Safe for concurrent use.
type Merger struct {
	mu       sync.Mutex
	sessions map[string]*seg.Seg
	checked  int // sessions inspected by the latest Append locate (hash: 1)
}

// New returns an empty Merger.
func New() *Merger { return &Merger{sessions: map[string]*seg.Seg{}} }

// Append validates, locates the session by Sid hash, and records the
// event. Any rejection happens before state mutation (no trace left).
func (m *Merger) Append(sid string, seq, value int) error {
	if sid == "" {
		return ErrEmptySid
	}
	if seq <= 0 {
		return seg.ErrBadSeq
	}
	if value < 0 || value > 9 {
		return seg.ErrBadValue
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked = 1 // map hash lookup inspects exactly one session, O(1)
	s, ok := m.sessions[sid]
	if !ok {
		s = seg.New()
		m.sessions[sid] = s
	}
	return s.Append(seq, value)
}

// Close freezes the session iff seq 1..n are exactly present.
func (m *Merger) Close(sid string, n int) error {
	if sid == "" {
		return ErrEmptySid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok {
		return seg.ErrIncomplete
	}
	return s.Close(n)
}

// Result returns the frozen result and whether the session is closed.
// An unknown Sid is simply not closed, not an error.
func (m *Merger) Result(sid string) (int, bool, error) {
	if sid == "" {
		return 0, false, ErrEmptySid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sid]; ok {
		r, closed := s.Result()
		return r, closed, nil
	}
	return 0, false, nil
}

// Seen returns the session's recorded seq numbers in ascending order.
func (m *Merger) Seen(sid string) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sid]; ok {
		return s.Seen()
	}
	return nil
}
