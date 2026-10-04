// Package override manages preemptive and shifting emergency overrides.
package override

import "ontology/internal/timeline"

// Channel is a shared linear-channel timeline.
type Channel = timeline.Timeline

// Preempt inserts a preemptive override: covered content keeps playing from
// where it was when the override ends; nothing moves.
func Preempt(c *Channel, now int64, id string, start, dur int64) error {
	return c.Override(now, id, start, dur, true)
}

// Shift inserts a shifting override at start, pushing later content back by dur.
func Shift(c *Channel, now int64, id string, start, dur int64) error {
	return c.Override(now, id, start, dur, false)
}

// Cancel truncates every segment belonging to id at now.
func Cancel(c *Channel, now int64, id string) error {
	return c.Cancel(now, id)
}

// Errors.
var (
	ErrBadArg     = timeline.ErrBadArg
	ErrClockBack  = timeline.ErrClockBack
	ErrDuplicate  = timeline.ErrDuplicate
	ErrPast       = timeline.ErrPast
	ErrConflict   = timeline.ErrOverride
	ErrInFixed    = timeline.ErrInFixed
	ErrCrowdFixed = timeline.ErrCrowdFixed
	ErrNotFound   = timeline.ErrNotFound
	ErrEnded      = timeline.ErrEnded
)
