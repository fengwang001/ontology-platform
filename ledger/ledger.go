// Package ledger is the quota ledger: used, available, self-check.
// It is not goroutine-safe; callers (package sem) hold the lock.
package ledger

import "errors"

// ErrOverRelease reports a release that would drive usage below zero.
var ErrOverRelease = errors.New("ledger: release exceeds held quota")

// Ledger tracks used quota against a fixed capacity.
type Ledger struct {
	used int64
	cap  int64
}

// New returns a Ledger with the given capacity.
func New(capacity int64) *Ledger { return &Ledger{cap: capacity} }

// Used returns the currently held quota.
func (l *Ledger) Used() int64 { return l.used }

// Cap returns the capacity.
func (l *Ledger) Cap() int64 { return l.cap }

// Avail returns the remaining quota.
func (l *Ledger) Avail() int64 { return l.cap - l.used }

// Take holds n more quota; the caller guarantees Avail() >= n.
func (l *Ledger) Take(n int64) { l.used += n }

// Give returns n quota; ErrOverRelease leaves the ledger unchanged.
func (l *Ledger) Give(n int64) error {
	if n > l.used {
		return ErrOverRelease
	}
	l.used -= n
	return nil
}

// Check reports the invariant used in [0, Cap].
func (l *Ledger) Check() bool { return l.used >= 0 && l.used <= l.cap }
