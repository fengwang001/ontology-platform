package gesture

import (
	"errors"
	"sync"
)

var (
	ErrInvalidDebounce           = errors.New("debounce count must be at least 1")
	ErrInvalidLongPressThreshold = errors.New("long-press threshold must be at least 1")
	ErrInvalidDoubleClickWindow  = errors.New("double-click window must be non-negative")
	ErrInvalidLevel              = errors.New("level must be 0 or 1")
)

type EventKind string

const (
	Press       EventKind = "Press"
	Release     EventKind = "Release"
	LongPress   EventKind = "LongPress"
	SingleClick EventKind = "SingleClick"
	DoubleClick EventKind = "DoubleClick"
)

type Event struct {
	Kind  EventKind
	Index int
}

type Stats struct {
	ShortPresses    int
	SingleClicks    int
	DoubleClicks    int
	CancelledClicks int
	PendingClicks   int
}

type Recognizer struct {
	mu        sync.Mutex
	d         int
	l         int
	w         int
	next      int
	stable    int
	diffRun   int
	pressed   bool
	pressAt   int
	longDone  bool
	pending   bool
	releaseAt int
	second    bool
	short     int
	singles   int
	doubles   int
	canceled  int
}

func New(debounce, longPressThreshold, doubleClickWindow int) (*Recognizer, error) {
	if debounce < 1 {
		return nil, ErrInvalidDebounce
	}
	if longPressThreshold < 1 {
		return nil, ErrInvalidLongPressThreshold
	}
	if doubleClickWindow < 0 {
		return nil, ErrInvalidDoubleClickWindow
	}

	return &Recognizer{
		d: debounce,
		l: longPressThreshold,
		w: doubleClickWindow,
	}, nil
}

func (r *Recognizer) Sample(level int) ([]Event, error) {
	if level != 0 && level != 1 {
		return nil, ErrInvalidLevel
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	index := r.next
	r.next++
	events := r.eventsDue(index)
	events = append(events, r.debounce(index, level)...)
	return events, nil
}

func (r *Recognizer) Run(levels []int) ([]Event, error) {
	for _, level := range levels {
		if level != 0 && level != 1 {
			return nil, ErrInvalidLevel
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	events := make([]Event, 0)
	for _, level := range levels {
		index := r.next
		r.next++
		events = append(events, r.eventsDue(index)...)
		events = append(events, r.debounce(index, level)...)
	}
	return events, nil
}

func (r *Recognizer) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending := 0
	if r.pending {
		pending = 1
	}
	return Stats{
		ShortPresses:    r.short,
		SingleClicks:    r.singles,
		DoubleClicks:    r.doubles,
		CancelledClicks: r.canceled,
		PendingClicks:   pending,
	}
}

func (r *Recognizer) eventsDue(index int) []Event {
	events := make([]Event, 0, 2)

	if r.pressed && !r.longDone && index == r.pressAt+r.l {
		events = append(events, Event{Kind: LongPress, Index: index})
		r.longDone = true
		if r.second {
			r.pending = false
			r.second = false
			r.canceled++
		}
	}

	if r.pending && !r.second && index == r.releaseAt+r.w+1 {
		events = append(events, Event{Kind: SingleClick, Index: index})
		r.pending = false
		r.singles++
	}

	return events
}

func (r *Recognizer) debounce(index, level int) []Event {
	if level == r.stable {
		r.diffRun = 0
		return nil
	}

	r.diffRun++
	if r.diffRun < r.d {
		return nil
	}

	r.stable ^= 1
	r.diffRun = 0

	if r.stable == 1 {
		return r.press(index)
	}
	return r.release(index)
}

func (r *Recognizer) press(index int) []Event {
	r.pressed = true
	r.pressAt = index
	r.longDone = false

	isSecondClick := r.pending && index-r.releaseAt <= r.w
	r.second = isSecondClick
	if !isSecondClick {
		r.pending = false
	}

	return []Event{{Kind: Press, Index: index}}
}

func (r *Recognizer) release(index int) []Event {
	r.pressed = false
	shortPress := index-r.pressAt < r.l
	r.longDone = false
	r.second = false
	events := []Event{{Kind: Release, Index: index}}

	if !shortPress {
		r.pending = false
		return events
	}

	r.short++
	if r.pending {
		events = append(events, Event{Kind: DoubleClick, Index: index})
		r.pending = false
		r.doubles++
		return events
	}

	r.pending = true
	r.releaseAt = index
	return events
}
