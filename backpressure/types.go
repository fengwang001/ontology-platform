// Package backpressure implements a watermark controller with hysteresis.
//
// The controller tracks a buffer occupancy level and switches between
// Flowing and Paused states, notifying registered observers on every
// real transition. It does not implement a queue, networking, or
// rate limiting.
package backpressure

import "errors"

// ErrBadWatermark is returned by New when the watermark configuration
// is invalid.
var ErrBadWatermark = errors.New("backpressure: bad watermark")

// State is the flow state of the controller.
type State int

const (
	// Flowing means producers may keep adding.
	Flowing State = iota
	// Paused means producers should stop adding.
	Paused
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case Flowing:
		return "Flowing"
	case Paused:
		return "Paused"
	default:
		return "Unknown"
	}
}

// Report is a snapshot of the controller's counters and level.
type Report struct {
	Level    int64 // current occupancy
	State    State
	Pauses   int   // total transitions into Paused
	Resumes  int   // total transitions back to Flowing
	Rejected int64 // total amount rejected for exceeding the hard limit
}
