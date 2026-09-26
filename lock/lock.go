// Package lock validates Acquire/Release calls and orchestrates the
// priority-inheritance core. It depends only on package pip.
package lock

import (
	"errors"

	"ontology/pip"
)

// Sentinel errors, one per rejectable fault; all mutually distinct.
var (
	ErrInvalidPriority  = errors.New("lock: priority must be >= 1")
	ErrDuplicateAcquire = errors.New("lock: task already holds or waits")
	ErrNotHolder        = errors.New("lock: release by non-holder")
)

// Lock serializes validation and state changes. Not goroutine-safe;
// callers serialize.
type Lock struct{ c *pip.Core }

// New returns an idle lock.
func New() *Lock { return &Lock{c: pip.NewCore()} }

// Acquire validates, then takes or queues. A rejected call changes nothing.
func (l *Lock) Acquire(id, prio int) error {
	if prio < 1 {
		return ErrInvalidPriority
	}
	if l.c.IsHolder(id) || l.c.Waiting(id) {
		return ErrDuplicateAcquire
	}
	l.c.Acquire(id, prio)
	return nil
}

// Release validates, then hands off or idles. A rejected call changes nothing.
func (l *Lock) Release(id int) error {
	if !l.c.IsHolder(id) {
		return ErrNotHolder
	}
	l.c.Release()
	return nil
}

// Holder reports the current holder.
func (l *Lock) Holder() (int, bool) { return l.c.Holder() }

// Effective reports the holder's effective priority (0 when idle).
func (l *Lock) Effective() int { return l.c.Effective() }
