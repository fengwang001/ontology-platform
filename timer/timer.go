// Package timer provides a hierarchical timing wheel with cancellation
// groups. All time comes from an injected clock.
package timer

import (
	"errors"
	"sync"

	"ontology/clock"
	"ontology/group"
	"ontology/wheel"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrTooManyTimers = errors.New("timer: too many timers")
	ErrGroupCanceled = errors.New("timer: group canceled")
	ErrClockBackward = errors.New("timer: clock moved backward")
)

// Handle identifies a registered timer.
type Handle struct {
	e *entry
}

type entry struct {
	node  wheel.Node
	dl    int64 // absolute deadline, ns
	g     *group.Group
	state int32 // 0 pending, 1 fired, 2 stopped
}

// Stats reports cumulative counters.
type Stats struct {
	Fired     int64
	Stopped   int64
	Panics    int64
	Live      int64
	SlotOps   int // slots visited by the most recent Advance
	CancelOps int // nodes visited by the most recent Cancel
}

// Timer is a hierarchical timing wheel bound to an injected clock.
type Timer struct {
	mu   sync.Mutex
	clk  clock.Clock
	wh   *wheel.Wheel
	tick int64
	max  int
	seq  uint64
	live int
	st   Stats
	last *group.Group
}

// New creates a timer: clk is the injected clock, tick the resolution in
// nanoseconds, slots per level, levels, max the live-timer capacity.
func New(clk clock.Clock, tick int64, slots, levels, max int) (*Timer, error) {
	wh, err := wheel.New(tick, slots, levels)
	if err != nil {
		return nil, err
	}
	return &Timer{clk: clk, wh: wh, tick: tick, max: max}, nil
}
