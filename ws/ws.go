// Package ws delays the verdict on trailing spaces and tabs: a run of
// whitespace is only known to be line-trailing once a line ending or the
// stream end is reached.
package ws

import "errors"

// ErrLimit means the pending whitespace run exceeded the configured bound.
var ErrLimit = errors.New("ws: pending whitespace limit exceeded")

// IsSpace reports whether b is a space or a tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Tracker buffers one unresolved run of spaces/tabs. Only a single run can be
// pending at a time: any non-space byte either keeps or discards the run.
type Tracker struct {
	buf   []byte
	start int
	limit int
}

// New returns a Tracker whose pending run may hold at most limit bytes;
// limit <= 0 means unlimited.
func New(limit int) *Tracker { return &Tracker{limit: limit} }

// Len reports the number of buffered whitespace bytes.
func (t *Tracker) Len() int { return len(t.buf) }

// Empty reports whether no whitespace is pending.
func (t *Tracker) Empty() bool { return len(t.buf) == 0 }

// Start reports the original input offset at which the run began.
func (t *Tracker) Start() int { return t.start }

// Add appends one whitespace byte b at original input offset off.
// It returns ErrLimit if the configured bound is exceeded.
func (t *Tracker) Add(b byte, off int) error {
	if t.limit > 0 && len(t.buf) >= t.limit {
		return ErrLimit
	}
	if len(t.buf) == 0 {
		t.start = off
	}
	t.buf = append(t.buf, b)
	return nil
}

// Keep returns the buffered bytes because a non-whitespace byte proved the run
// was interior, and clears the tracker.
func (t *Tracker) Keep() []byte {
	out := t.buf
	t.buf = nil
	return out
}

// Drop discards the buffered bytes because a line ending (or stream end)
// proved the run was line-trailing, and clears the tracker.
func (t *Tracker) Drop() { t.buf = nil }
