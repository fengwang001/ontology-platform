package watchdog

import (
	"errors"
	"sync"
)

var (
	ErrNegativeOpen  = errors.New("watchdog: open must not be negative")
	ErrInvalidWindow = errors.New("watchdog: close must be greater than open")
	ErrNegativePre   = errors.New("watchdog: pre must not be negative")
	ErrPreTooLarge   = errors.New("watchdog: pre must be less than close")
	ErrNoClock       = errors.New("watchdog: no clock injected")
)

type EventKind string

const (
	Warning EventKind = "warning"
	Reset   EventKind = "reset"
)

type ResetReason string

const (
	Early   ResetReason = "Early"
	Timeout ResetReason = "Timeout"
)

type Rejection string

const (
	RejectTimeRollback Rejection = "time rollback"
	RejectResetFeed    Rejection = "feed while reset"
	RejectEarlyFeed    Rejection = "early feed"
	RejectRestartArmed Rejection = "restart while armed"
)

type Clock interface {
	// Now returns the injected current time as an integer timestamp.
	Now() int64
}

// Event is one warning or reset record in the global event table.
type Event struct {
	Time         int64
	Kind         EventKind
	ResetReason  ResetReason
	FeedRejected bool
}

// Outcome reports one Feed, Tick, or Restart operation.
type Outcome struct {
	Time      int64
	Rejected  bool
	Rejection Rejection
	Events    []Event
}

// Watchdog is a concurrent-safe window watchdog with explicit integer time.
type Watchdog struct {
	mu      sync.Mutex
	clock   Clock
	t0      int64
	open    int64
	closeAt int64
	pre     int64
	lastOp  int64
	f       int64
	warned  bool
	reset   bool
	events  []Event
}

// New creates an armed watchdog starting at t0.
func New(t0, open, closeAt, pre int64) (*Watchdog, error) {
	if open < 0 {
		return nil, ErrNegativeOpen
	}
	if closeAt <= open {
		return nil, ErrInvalidWindow
	}
	if pre < 0 {
		return nil, ErrNegativePre
	}
	if pre >= closeAt {
		return nil, ErrPreTooLarge
	}

	return &Watchdog{
		t0:      t0,
		open:    open,
		closeAt: closeAt,
		pre:     pre,
		lastOp:  t0,
		f:       t0,
	}, nil
}

// NewWithClock creates a watchdog whose *Now methods obtain time from clock.
func NewWithClock(t0, open, closeAt, pre int64, clock Clock) (*Watchdog, error) {
	w, err := New(t0, open, closeAt, pre)
	if err != nil {
		return nil, err
	}
	w.clock = clock
	return w, nil
}

// Feed records due events first, then accepts or rejects a feed at t.
func (w *Watchdog) Feed(t int64) Outcome {
	w.mu.Lock()
	defer w.mu.Unlock()

	outcome, ok := w.begin(t)
	if !ok {
		return outcome
	}

	if w.reset {
		return w.reject(t, RejectResetFeed, outcome.Events)
	}

	elapsed := t - w.f
	if elapsed < w.open {
		resetEvent := Event{
			Time:         t,
			Kind:         Reset,
			ResetReason:  Early,
			FeedRejected: true,
		}
		w.events = append(w.events, resetEvent)
		w.reset = true
		outcome.Events = append(outcome.Events, resetEvent)
		return w.rejectWithOutcome(t, RejectEarlyFeed, outcome)
	}

	w.f = t
	w.warned = false
	w.lastOp = t
	return outcome
}

// Tick records all warning or timeout events due by t without feeding.
func (w *Watchdog) Tick(t int64) Outcome {
	w.mu.Lock()
	defer w.mu.Unlock()

	outcome, ok := w.begin(t)
	if !ok {
		return outcome
	}
	w.lastOp = t
	return outcome
}

// Restart records due events first, then succeeds only while latched reset.
func (w *Watchdog) Restart(t int64) Outcome {
	w.mu.Lock()
	defer w.mu.Unlock()

	outcome, ok := w.begin(t)
	if !ok {
		return outcome
	}

	if !w.reset {
		return w.reject(t, RejectRestartArmed, outcome.Events)
	}

	w.reset = false
	w.f = t
	w.warned = false
	w.lastOp = t
	return outcome
}

// Events returns a copied global event table in nondecreasing time order.
func (w *Watchdog) Events() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneEvents(w.events)
}

func (w *Watchdog) FeedNow() (Outcome, error) {
	if w.clock == nil {
		return Outcome{}, ErrNoClock
	}
	return w.Feed(w.clock.Now()), nil
}

func (w *Watchdog) TickNow() (Outcome, error) {
	if w.clock == nil {
		return Outcome{}, ErrNoClock
	}
	return w.Tick(w.clock.Now()), nil
}

func (w *Watchdog) RestartNow() (Outcome, error) {
	if w.clock == nil {
		return Outcome{}, ErrNoClock
	}
	return w.Restart(w.clock.Now()), nil
}

func (w *Watchdog) begin(t int64) (Outcome, bool) {
	outcome := Outcome{Time: t, Events: []Event{}}
	if t < w.lastOp {
		outcome.Rejected = true
		outcome.Rejection = RejectTimeRollback
		return outcome, false
	}

	outcome.Events = w.catchUp(t)
	if outcome.Events == nil {
		outcome.Events = []Event{}
	}
	return outcome, true
}

func (w *Watchdog) catchUp(t int64) []Event {
	if w.reset {
		return []Event{}
	}

	var recorded []Event
	if w.pre > 0 && !w.warned {
		warningTime := w.f + w.closeAt - w.pre
		if warningTime <= t {
			event := Event{Time: warningTime, Kind: Warning}
			w.events = append(w.events, event)
			w.warned = true
			recorded = append(recorded, event)
		}
	}

	timeoutTime := w.f + w.closeAt
	if timeoutTime <= t {
		event := Event{Time: timeoutTime, Kind: Reset, ResetReason: Timeout}
		w.events = append(w.events, event)
		w.reset = true
		recorded = append(recorded, event)
	}

	return cloneEvents(recorded)
}

func (w *Watchdog) reject(t int64, reason Rejection, recorded []Event) Outcome {
	w.lastOp = t
	return Outcome{
		Time:      t,
		Rejected:  true,
		Rejection: reason,
		Events:    cloneEvents(recorded),
	}
}

func (w *Watchdog) rejectWithOutcome(t int64, reason Rejection, outcome Outcome) Outcome {
	w.lastOp = t
	outcome.Rejected = true
	outcome.Rejection = reason
	outcome.Events = cloneEvents(outcome.Events)
	return outcome
}

func cloneEvents(events []Event) []Event {
	if len(events) == 0 {
		return []Event{}
	}
	copied := make([]Event, len(events))
	copy(copied, events)
	return copied
}
