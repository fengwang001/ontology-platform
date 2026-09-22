// Package scheduler is the public hierarchical timing-wheel scheduler.
// Time is fully injected: it starts at a constructor-supplied instant and
// moves only via Advance. All exported methods are goroutine-safe.
package scheduler

import (
	"sync"

	"ontology/cascade"
	"ontology/timer"
	"ontology/wheel"
)

// Event is delivered to Config.OnFire when a timer fires.
type Event struct {
	ID       uint64
	Seq      uint64
	Deadline int64
	Payload  any
}

// Config configures a Scheduler. Zero fields get defaults: Slots=64,
// Levels=4, MaxAdvance=1<<20, MaxTimers=1<<20, MaxDelay=Slots^Levels-Slots^(Levels-1).
type Config struct {
	Slots      int
	Levels     int
	MaxAdvance int64
	MaxTimers  int
	MaxDelay   int64
	// OnFire is called without any scheduler lock held; it may call
	// Add/Cancel/Reset but must not call Advance.
	OnFire func(Event)
}

// Scheduler manages the wheel levels and the injected clock.
type Scheduler struct {
	cfg Config

	advMu sync.Mutex // serializes Advance; acquired before mu
	mu    sync.Mutex
	now   int64

	wheels  []*wheel.Wheel
	timers  map[uint64]*timer.Timer // includes fired/cancelled tombstones
	pending []pendingEntry          // delay-0 timers, fired next Advance
	live    int
	nextID  uint64
	nextSeq uint64

	touchedSlots  int64 // slots touched by the most recent Advance
	touchedTimers int64 // timers examined by the most recent Advance
}

type pendingEntry struct {
	t   *timer.Timer
	gen uint64
}

// New creates a Scheduler whose injected clock starts at start (>= 0).
func New(start int64, cfg Config) (*Scheduler, error) {
	if start < 0 {
		return nil, ErrInvalidConfig
	}
	if cfg.Slots == 0 {
		cfg.Slots = 64
	}
	if cfg.Levels == 0 {
		cfg.Levels = 4
	}
	if cfg.Slots < 2 || cfg.Levels < 1 {
		return nil, ErrInvalidConfig
	}
	if cfg.MaxAdvance == 0 {
		cfg.MaxAdvance = 1 << 20
	}
	if cfg.MaxTimers == 0 {
		cfg.MaxTimers = 1 << 20
	}
	ws := make([]*wheel.Wheel, cfg.Levels)
	tick := int64(1)
	for l := range ws {
		ws[l] = wheel.New(cfg.Slots, tick, start)
		next := tick * int64(cfg.Slots)
		if next < tick { // int64 overflow: too many levels
			return nil, ErrInvalidConfig
		}
		tick = next
	}
	if cfg.MaxDelay == 0 {
		cfg.MaxDelay = tick - tick/int64(cfg.Slots)
	}
	return &Scheduler{
		cfg: cfg, now: start, wheels: ws,
		timers: make(map[uint64]*timer.Timer), nextID: 1,
	}, nil
}

// Now returns the current injected time.
func (s *Scheduler) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Add registers a timer firing after delay ticks and returns its handle.
// A delay of 0 fires on the first tick of the next Advance.
func (s *Scheduler) Add(delay int64, payload any) (uint64, error) {
	if delay < 0 {
		return 0, ErrNegativeDelay
	}
	if delay > s.cfg.MaxDelay {
		return 0, ErrDelayTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live >= s.cfg.MaxTimers {
		return 0, ErrTooManyTimers
	}
	id := s.nextID
	s.nextID++
	s.nextSeq++
	t := timer.New(id, s.nextSeq, s.now+delay, payload)
	if delay == 0 {
		s.pending = append(s.pending, pendingEntry{t, t.Gen()})
	} else if err := s.placeLocked(t); err != nil {
		return 0, err // nothing recorded: no half-registered timer
	}
	s.timers[id] = t
	s.live++
	return id, nil
}

// Cancel cancels a live timer. It is idempotent with distinct, decidable
// results: nil on success, ErrTimerCancelled on repeat, ErrTimerFired if
// already fired, ErrTimerNotFound for unknown handles.
func (s *Scheduler) Cancel(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.timers[id]
	if !ok {
		return ErrTimerNotFound
	}
	switch t.State() {
	case timer.Fired:
		return ErrTimerFired
	case timer.Cancelled:
		return ErrTimerCancelled
	}
	t.Cancel()
	t.Detach()
	s.live--
	return nil
}

// Reset re-arms a live timer with a new delay counted from now.
func (s *Scheduler) Reset(id uint64, delay int64) error {
	if delay < 0 {
		return ErrNegativeDelay
	}
	if delay > s.cfg.MaxDelay {
		return ErrDelayTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.timers[id]
	if !ok {
		return ErrTimerNotFound
	}
	switch t.State() {
	case timer.Fired:
		return ErrTimerFired
	case timer.Cancelled:
		return ErrTimerCancelled
	}
	deadline := s.now + delay
	if delay > 0 {
		if _, _, ok := cascade.Place(s.wheels, deadline); !ok {
			return ErrDelayTooLarge // checked before any state change
		}
	}
	t.Detach()
	t.BumpGen()
	t.Deadline = deadline
	if delay == 0 {
		s.pending = append(s.pending, pendingEntry{t, t.Gen()})
		return nil
	}
	return s.placeLocked(t)
}

// placeLocked inserts t into the wheel level/slot covering its deadline.
func (s *Scheduler) placeLocked(t *timer.Timer) error {
	level, idx, ok := cascade.Place(s.wheels, t.Deadline)
	if !ok {
		return ErrDelayTooLarge
	}
	s.wheels[level].InsertAt(idx, t.ID, t)
	t.Attach(s.wheels[level].Slot(idx), level, idx)
	return nil
}
