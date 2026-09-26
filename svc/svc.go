// Package svc manages servers on top of the lc core: it validates
// indices and rejects connection-count underflow before touching the
// core. It is not concurrency-safe; callers must serialize access.
package svc

import (
	"errors"

	"ontology/lc"
)

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrBadIndex  = errors.New("svc: server index out of range")
	ErrUnderflow = errors.New("svc: release on server with zero connections")
)

// Manager validates operations before applying them to the lc core.
type Manager struct {
	core *lc.Core
}

// NewManager wraps an lc core for n servers. Requires n >= 1.
func NewManager(n int) *Manager {
	return &Manager{core: lc.New(n)}
}

// Size reports the number of servers.
func (m *Manager) Size() int { return m.core.Size() }

// valid reports whether i is a legal server index.
func (m *Manager) valid(i int) bool { return 0 <= i && i < m.core.Size() }

// Acquire adds one connection to server i.
func (m *Manager) Acquire(i int) error {
	if !m.valid(i) {
		return ErrBadIndex
	}
	m.core.Incr(i)
	return nil
}

// Release removes one connection from server i, refusing underflow.
func (m *Manager) Release(i int) error {
	if !m.valid(i) {
		return ErrBadIndex
	}
	if m.core.Count(i) < 1 {
		return ErrUnderflow
	}
	m.core.Decr(i)
	return nil
}

// Pick returns the least-connected server's index (smallest on ties).
func (m *Manager) Pick() int { return m.core.Pick() }

// Count reports server i's active connections.
func (m *Manager) Count(i int) (int, error) {
	if !m.valid(i) {
		return 0, ErrBadIndex
	}
	return m.core.Count(i), nil
}
