// Package cep implements a keyed complex-event pattern matcher that pairs
// a "first" event with a later "second" event of the same key inside a
// closed time window, under strict or relaxed contiguity.
package cep

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
)

// Mode selects the contiguity requirement between the first and second event.
type Mode int

const (
	// Strict requires the second event to be the very next event of the same key.
	Strict Mode = iota
	// Relaxed allows arbitrary events between the first and second event.
	Relaxed
)

func (m Mode) String() string {
	switch m {
	case Strict:
		return "strict"
	case Relaxed:
		return "relaxed"
	default:
		return "unknown"
	}
}

// Distinguishable rejection reasons returned by NewMatcher and ProcessBatch.
var (
	ErrInvalidWindow     = errors.New("cep: window must be a positive integer")
	ErrInvalidMaxPending = errors.New("cep: max pending must be a positive integer")
	ErrInvalidMode       = errors.New("cep: unknown contiguity mode")
	ErrEmptyFirstType    = errors.New("cep: first type must not be empty")
	ErrEmptySecondType   = errors.New("cep: second type must not be empty")
	ErrTypeConflict      = errors.New("cep: first type and second type must differ")
	ErrEmptyKey          = errors.New("cep: event key must not be empty")
	ErrEmptyType         = errors.New("cep: event type must not be empty")
	ErrTimeRegression    = errors.New("cep: event time regressed within key")
	ErrQueueFull         = errors.New("cep: pending queue limit exceeded")
)

// Event is a single occurrence in a keyed stream. Time must be monotonically
// non-decreasing per key; the unit is caller-defined but must match Window.
type Event struct {
	Key  string
	Type string
	Time int64
}

// Match is an emitted pair. Delta is Second.Time - First.Time and always
// satisfies 0 <= Delta <= window (closed interval).
type Match struct {
	Key    string
	First  Event
	Second Event
	Delta  int64
}

// Config configures a Matcher. All fields are validated by NewMatcher.
type Config struct {
	FirstType  string
	SecondType string
	Window     int64
	MaxPending int
	Mode       Mode
	// Logger receives input, pairing and decision-basis lines.
	// If nil, a default logger writing to stderr is used.
	Logger *log.Logger
}

type keyState struct {
	last    Event
	hasLast bool
	pending []Event
}

// Matcher is safe for concurrent use.
type Matcher struct {
	cfg    Config
	logger *log.Logger

	mu      sync.RWMutex
	keys    map[string]*keyState
	matches []Match
}

// NewMatcher validates cfg and returns a ready Matcher.
func NewMatcher(cfg Config) (*Matcher, error) {
	if cfg.FirstType == "" {
		return nil, ErrEmptyFirstType
	}
	if cfg.SecondType == "" {
		return nil, ErrEmptySecondType
	}
	if cfg.FirstType == cfg.SecondType {
		return nil, fmt.Errorf("%w: %q", ErrTypeConflict, cfg.FirstType)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidWindow, cfg.Window)
	}
	if cfg.MaxPending <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidMaxPending, cfg.MaxPending)
	}
	if cfg.Mode != Strict && cfg.Mode != Relaxed {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidMode, int(cfg.Mode))
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[cep] ", log.LstdFlags|log.Lmicroseconds)
	}
	m := &Matcher{
		cfg:    cfg,
		logger: logger,
		keys:   make(map[string]*keyState),
	}
	m.logger.Printf("matcher created: first=%q second=%q window=%d maxPending=%d mode=%s",
		cfg.FirstType, cfg.SecondType, cfg.Window, cfg.MaxPending, cfg.Mode)
	return m, nil
}

// ProcessBatch validates and applies a batch atomically: on any rejection the
// pending queue, last events and emitted matches are left untouched.
func (m *Matcher) ProcessBatch(events []Event) ([]Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys := make(map[string]*keyState, len(m.keys))
	for k, s := range m.keys {
		copied := *s
		copied.pending = append([]Event(nil), s.pending...)
		keys[k] = &copied
	}

	var emitted []Match
	for i, e := range events {
		if err := m.process(keys, e, &emitted); err != nil {
			m.logger.Printf("batch rejected at index %d: event=%+v reason=%v", i, e, err)
			return nil, err
		}
	}

	m.keys = keys
	m.matches = append(m.matches, emitted...)
	return append([]Match(nil), emitted...), nil
}

// process applies a single event to the working state, appending any match.
func (m *Matcher) process(keys map[string]*keyState, e Event, emitted *[]Match) error {
	if e.Key == "" {
		return ErrEmptyKey
	}
	if e.Type == "" {
		return fmt.Errorf("%w: key=%q time=%d", ErrEmptyType, e.Key, e.Time)
	}
	ks := keys[e.Key]
	if ks == nil {
		ks = &keyState{}
		keys[e.Key] = ks
	}
	if ks.hasLast && e.Time < ks.last.Time {
		return fmt.Errorf("%w: key=%q last=%d got=%d", ErrTimeRegression, e.Key, ks.last.Time, e.Time)
	}

	m.logger.Printf("input: key=%q type=%q time=%d mode=%s", e.Key, e.Type, e.Time, m.cfg.Mode)

	switch m.cfg.Mode {
	case Strict:
		m.processStrict(ks, e, emitted)
	case Relaxed:
		if err := m.processRelaxed(ks, e, emitted); err != nil {
			return err
		}
	}

	ks.last = e
	ks.hasLast = true
	return nil
}

func (m *Matcher) processStrict(ks *keyState, e Event, emitted *[]Match) {
	if e.Type != m.cfg.SecondType {
		return
	}
	if !ks.hasLast || ks.last.Type != m.cfg.FirstType {
		m.logger.Printf("no pair: key=%q second at %d not immediately after a %q event",
			e.Key, e.Time, m.cfg.FirstType)
		return
	}
	delta := e.Time - ks.last.Time
	if delta > m.cfg.Window {
		m.logger.Printf("no pair: key=%q delta=%d exceeds window=%d", e.Key, delta, m.cfg.Window)
		return
	}
	m.emit(ks.last, e, emitted)
}

func (m *Matcher) processRelaxed(ks *keyState, e Event, emitted *[]Match) error {
	kept := ks.pending[:0]
	for _, p := range ks.pending {
		if e.Time-p.Time > m.cfg.Window {
			m.logger.Printf("expired: key=%q pending %q at %d can no longer match (now=%d window=%d)",
				e.Key, p.Type, p.Time, e.Time, m.cfg.Window)
			continue
		}
		kept = append(kept, p)
	}
	ks.pending = kept

	switch e.Type {
	case m.cfg.FirstType:
		if len(ks.pending) >= m.cfg.MaxPending {
			return fmt.Errorf("%w: key=%q limit=%d", ErrQueueFull, e.Key, m.cfg.MaxPending)
		}
		ks.pending = append(ks.pending, e)
		m.logger.Printf("queued: key=%q pending=%d/%d", e.Key, len(ks.pending), m.cfg.MaxPending)
	case m.cfg.SecondType:
		if len(ks.pending) == 0 {
			m.logger.Printf("no pair: key=%q no pending %q event", e.Key, m.cfg.FirstType)
			return nil
		}
		first := ks.pending[0]
		ks.pending = ks.pending[1:]
		m.emit(first, e, emitted)
	}
	return nil
}

func (m *Matcher) emit(first, second Event, emitted *[]Match) {
	match := Match{
		Key:    first.Key,
		First:  first,
		Second: second,
		Delta:  second.Time - first.Time,
	}
	*emitted = append(*emitted, match)
	m.logger.Printf("paired: key=%q first=%q@%d second=%q@%d delta=%d window=%d (closed interval: delta<=window)",
		match.Key, first.Type, first.Time, second.Type, second.Time, match.Delta, m.cfg.Window)
}

// Matches returns a copy of all matches emitted so far, in emission order.
func (m *Matcher) Matches() []Match {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Match(nil), m.matches...)
}

// Pending returns a copy of the unmatched first-type events queued for key.
func (m *Matcher) Pending(key string) []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ks := m.keys[key]
	if ks == nil {
		return nil
	}
	return append([]Event(nil), ks.pending...)
}

// LastEvent reports the most recent accepted event for key.
func (m *Matcher) LastEvent(key string) (Event, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ks := m.keys[key]
	if ks == nil || !ks.hasLast {
		return Event{}, false
	}
	return ks.last, true
}
