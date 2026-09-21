// Package backpressure implements a watermark controller with hysteresis.
//
// The controller tracks a buffer occupancy level and switches between
// Flowing and Paused states, notifying registered observers on every
// real transition. It does not implement a queue, networking, or rate
// limiting.
package backpressure

import (
	"errors"
	"sync"
)

// ErrBadWatermark is returned by New when the watermark configuration
// is invalid (negative values, low >= high, or high > hard).
var ErrBadWatermark = errors.New("backpressure: bad watermark")

// State is the flow state of the controller.
type State int

const (
	// Flowing means producers may keep sending.
	Flowing State = iota
	// Paused means producers should stop sending.
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

// Report is a point-in-time snapshot of the controller.
type Report struct {
	Level    int64 // current occupancy
	State    State
	Pauses   int   // total transitions into Paused
	Resumes  int   // total transitions back to Flowing
	Rejected int64 // total amount rejected for exceeding the hard limit
}

// Controller is a watermark controller with hysteresis. It is safe for
// concurrent use.
type Controller struct {
	mu        sync.Mutex // guards every field below
	low       int64
	high      int64
	hard      int64
	level     int64
	state     State
	pauses    int
	resumes   int
	rejected  int64
	callbacks []func(State)

	dispMu sync.Mutex // serializes callback dispatch in transition order
}

// New creates a Controller. It requires 0 <= low < high <= hard; any
// violation returns ErrBadWatermark and a nil controller.
func New(low, high, hard int64) (*Controller, error) {
	if low < 0 || high < 0 || hard < 0 || low >= high || high > hard {
		return nil, ErrBadWatermark
	}
	return &Controller{
		low:   low,
		high:  high,
		hard:  hard,
		state: Flowing,
	}, nil
}
