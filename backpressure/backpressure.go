// Package backpressure implements a watermark controller with hysteresis.
//
// The controller tracks a buffer occupancy level and switches between
// Flowing and Paused states: it pauses when the level reaches the high
// watermark and only resumes once the level drops to the low watermark.
// Observers are notified exactly once per real state transition.
package backpressure

import "errors"

// ErrBadWatermark is returned by New when the watermark configuration is
// invalid: any negative value, low >= high, or high > hard.
var ErrBadWatermark = errors.New("backpressure: bad watermark")

// State is the flow state of a Controller.
type State int

const (
	// Flowing means producers may keep writing.
	Flowing State = iota
	// Paused means producers should stop writing.
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

// Report is a point-in-time snapshot of a Controller.
type Report struct {
	Level    int64 // current occupancy
	State    State
	Pauses   int   // total transitions into Paused
	Resumes  int   // total transitions back to Flowing
	Rejected int64 // total amount rejected for exceeding the hard limit
}
