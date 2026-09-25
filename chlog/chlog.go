// Package chlog is the change log and its downstream view: it applies
// +/- changes in order to a key→sum view and enforces withdrawal
// consistency. It depends only on the standard library (the agg package
// emits the changes this log consumes).
package chlog

import "errors"

// ErrInconsistent reports a change that violates the at-most-one-value
// invariant: a + for a key already present, or a - withdrawing a value the
// key does not currently hold.
var ErrInconsistent = errors.New("chlog: inconsistent change")

// Change is one log entry: Plus=true sets the key to Sum, Plus=false
// withdraws exactly Sum.
type Change struct {
	Plus bool
	Key  string
	Sum  int64
}

// Log is an append-only change log with its materialized downstream view.
// It is not safe for concurrent use; callers (package api) serialize.
type Log struct {
	changes []Change
	view    map[string]int64
}

// New returns an empty log.
func New() *Log {
	return &Log{view: map[string]int64{}}
}

// Append validates c against the current downstream view, applies it and
// records it. A rejected change leaves the log and view untouched.
func (l *Log) Append(c Change) error {
	if c.Plus {
		if _, ok := l.view[c.Key]; ok {
			return ErrInconsistent // a key may hold at most one value
		}
		l.view[c.Key] = c.Sum
	} else {
		cur, ok := l.view[c.Key]
		if !ok || cur != c.Sum {
			return ErrInconsistent // must withdraw exactly the current value
		}
		delete(l.view, c.Key)
	}
	l.changes = append(l.changes, c)
	return nil
}

// Len returns the number of recorded changes.
func (l *Log) Len() int { return len(l.changes) }

// Changes returns a copy of the log in arrival order.
func (l *Log) Changes() []Change {
	out := make([]Change, len(l.changes))
	copy(out, l.changes)
	return out
}

// View returns a copy of the materialized downstream view: keys currently
// present (between a + and its matching -).
func (l *Log) View() map[string]int64 {
	out := make(map[string]int64, len(l.view))
	for k, v := range l.view {
		out[k] = v
	}
	return out
}

// ReplayPrefix applies the first n changes to a fresh log and returns its
// materialized view; ErrInconsistent if any prefix entry is invalid.
func ReplayPrefix(changes []Change, n int) (map[string]int64, error) {
	p := New()
	for _, c := range changes[:n] {
		if err := p.Append(c); err != nil {
			return nil, err
		}
	}
	return p.View(), nil
}
