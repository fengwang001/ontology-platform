// Package slot manages regular program segments on a linear channel.
package slot

import "ontology/internal/timeline"

// Channel is a shared linear-channel timeline.
type Channel = timeline.Timeline

// Result re-exports a playout result for callers of this package.
type Result = timeline.Result

// New creates a channel with filler material of length f seconds.
func New(f int64) (*Channel, error) {
	if f < 1 || f > 1_000_000 {
		return nil, timeline.ErrBadArg
	}
	return timeline.New(f), nil
}

// Schedule places a regular program [start, start+dur); fixed programs never move.
func Schedule(c *Channel, now int64, id string, start, dur int64, fixed bool) error {
	return c.Schedule(now, id, start, dur, fixed)
}

// Errors.
var (
	ErrBadArg    = timeline.ErrBadArg
	ErrClockBack = timeline.ErrClockBack
	ErrDuplicate = timeline.ErrDuplicate
	ErrPast      = timeline.ErrPast
	ErrOverlap   = timeline.ErrOverlap
)
