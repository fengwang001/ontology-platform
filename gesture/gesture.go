// Package gesture debounces millisecond button levels and recognizes clicks.
package gesture

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidDebounce means the debounce threshold is below 1.
	ErrInvalidDebounce = errors.New("debounce count must be at least 1")
	// ErrInvalidLongPress means the long-press threshold is below 1.
	ErrInvalidLongPress = errors.New("long-press threshold must be at least 1")
	// ErrInvalidDoubleClickWindow means the double-click window is negative.
	ErrInvalidDoubleClickWindow = errors.New("double-click window must be non-negative")
	// ErrInvalidLevel means a sampled level is neither 0 nor 1.
	ErrInvalidLevel = errors.New("level must be 0 or 1")
)

// EventKind identifies the kind of a gesture event.
type EventKind string

const (
	Press       EventKind = "Press"
	Release     EventKind = "Release"
	LongPress   EventKind = "LongPress"
	SingleClick EventKind = "SingleClick"
	DoubleClick EventKind = "DoubleClick"
)

// Event is a gesture occurrence at the sample index where it is emitted.
type Event struct {
	Kind  EventKind
	Index int
}

// Recognizer consumes one millisecond button level at a time.
type Recognizer struct {
	mu sync.Mutex

	d int
	l int
	w int

	sampleCount int
	stableLevel int
	diffCount   int

	pressed        bool
	pressIndex     int
	longPressFired bool

	pendingShort   bool
	secondHit      bool
	pendingRelease int
	discardedCount int
}

// New validates timing parameters and creates a Recognizer.
func New(debounce int, longPress int, doubleClickWindow int) (*Recognizer, error) {
	if debounce < 1 {
		return nil, ErrInvalidDebounce
	}
	if longPress < 1 {
		return nil, ErrInvalidLongPress
	}
	if doubleClickWindow < 0 {
		return nil, ErrInvalidDoubleClickWindow
	}

	return &Recognizer{
		d: debounce,
		l: longPress,
		w: doubleClickWindow,
	}, nil
}

// Sample consumes one level and returns all events emitted at that sample.
func (r *Recognizer) Sample(level int) ([]Event, error) {
	if level != 0 && level != 1 {
		return nil, ErrInvalidLevel
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	index := r.sampleCount
	r.sampleCount++

	var events []Event

	if r.pressed && !r.longPressFired && index == r.pressIndex+r.l {
		events = append(events, Event{Kind: LongPress, Index: index})
		r.longPressFired = true
		if r.secondHit {
			r.pendingShort = false
			r.secondHit = false
			r.discardedCount++
		}
	}

	if r.pendingShort && !r.pressed && index == r.pendingRelease+r.w+1 {
		events = append(events, Event{Kind: SingleClick, Index: index})
		r.pendingShort = false
	}

	if level == r.stableLevel {
		r.diffCount = 0
		return events, nil
	}

	r.diffCount++
	if r.diffCount < r.d {
		return events, nil
	}

	r.stableLevel = level
	r.diffCount = 0

	if level == 1 {
		r.pressed = true
		r.pressIndex = index
		r.longPressFired = false
		if r.pendingShort && index-r.pendingRelease <= r.w {
			r.secondHit = true
		} else {
			r.pendingShort = false
			r.secondHit = false
		}
		events = append(events, Event{Kind: Press, Index: index})
		return events, nil
	}

	pressIndex := r.pressIndex
	wasLongPress := r.longPressFired
	r.pressed = false
	r.longPressFired = false
	events = append(events, Event{Kind: Release, Index: index})

	if r.pendingShort {
		if r.secondHit {
			if index-pressIndex >= r.l {
				r.pendingShort = false
				r.secondHit = false
				r.discardedCount++
				return events, nil
			}

			events = append(events, Event{Kind: DoubleClick, Index: index})
			r.pendingShort = false
			r.secondHit = false
			return events, nil
		}

		if index-pressIndex >= r.l {
			r.pendingShort = false
			r.discardedCount++
			return events, nil
		}
	}

	if wasLongPress {
		return events, nil
	}

	if index-pressIndex >= r.l {
		return events, nil
	}

	r.pendingShort = true
	r.secondHit = false
	r.pendingRelease = index
	return events, nil
}

// Run validates and consumes a batch of levels as consecutive samples.
func (r *Recognizer) Run(levels []int) ([]Event, error) {
	for _, level := range levels {
		if level != 0 && level != 1 {
			return nil, ErrInvalidLevel
		}
	}

	events := make([]Event, 0)
	for _, level := range levels {
		newEvents, err := r.Sample(level)
		if err != nil {
			return nil, err
		}
		events = append(events, newEvents...)
	}

	return events, nil
}
