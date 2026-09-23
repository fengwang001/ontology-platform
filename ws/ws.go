// Package ws tracks an undecided run of trailing spaces/tabs.
//
// A run of spaces and tabs is only known to be trailing when the next byte is
// a line ending or the stream ends. The run is buffered by the caller; this
// package only counts it and enforces the configured limit.
package ws

import (
	"errors"
	"fmt"
)

// DefaultLimit is the default maximum undecided run length in bytes.
const DefaultLimit = 1 << 20

// ErrLimit is returned when an undecided whitespace run exceeds the limit.
var ErrLimit = errors.New("ws: undecided whitespace run exceeds limit")

// LimitError carries the original byte offset where the offending run began.
type LimitError struct {
	Start int64
	Run   int
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("ws: run of %d spaces/tabs at offset %d exceeds limit %d", e.Run, e.Start, e.Limit)
}

// Is marks LimitError as ErrLimit.
func (e *LimitError) Is(target error) bool { return target == ErrLimit }

// Tracker counts the current undecided run. Zero value is usable with
// DefaultLimit; construct with New for a custom limit.
type Tracker struct {
	n     int
	start int64
	limit int
}

// New returns a Tracker with the given run-length limit. A limit <= 0 means
// DefaultLimit.
func New(limit int) *Tracker {
	if limit <= 0 {
		limit = DefaultLimit
	}
	return &Tracker{limit: limit}
}

// IsWS reports whether b is a space or a tab.
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Add records one whitespace byte at original offset off. When the run starts
// (previous length 0), off is remembered for a possible LimitError. It returns
// a *LimitError when the limit is exceeded.
func (t *Tracker) Add(b byte, off int64) error {
	if t.limit == 0 {
		t.limit = DefaultLimit
	}
	if t.n == 0 {
		t.start = off
	}
	t.n++
	if t.n > t.limit {
		return &LimitError{Start: t.start, Run: t.n, Limit: t.limit}
	}
	return nil
}

// Flush ends the run (line ending or non-whitespace seen, or stream closed).
func (t *Tracker) Flush() { t.n = 0 }

// Len is the current undecided run length.
func (t *Tracker) Len() int { return t.n }

// Active reports whether a run is in progress.
func (t *Tracker) Active() bool { return t.n > 0 }

// Limit returns the configured limit.
func (t *Tracker) Limit() int {
	if t.limit <= 0 {
		return DefaultLimit
	}
	return t.limit
}
