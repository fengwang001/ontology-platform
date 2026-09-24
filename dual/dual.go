// Package dual coordinates the two watermarks: it owns the event-time
// Clock, the processing-time watermark PTW, the accepted event set and
// the late count. PTW never participates in lateness judgment.
package dual

import (
	"errors"
	"sort"
	"sync"

	"ontology/etime"
)

// Sentinel errors for rejected operations. Distinct from each other and
// from etime.ErrNegativeLateness.
var (
	ErrEmptyID             = errors.New("dual: event id must not be empty")
	ErrHeartbeatRegression = errors.New("dual: heartbeat ts below current PTW")
)

// Event is one accepted event.
type Event struct {
	Et int64
	ID string
}

// Engine is the dual-watermark coordinator. Safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	clock    *etime.Clock
	ptw      int64
	late     int64
	accepted []Event
}

// New returns an Engine with allowed lateness l. PTW starts at negInf.
func New(l int64) (*Engine, error) {
	c, err := etime.New(l)
	if err != nil {
		return nil, err
	}
	return &Engine{clock: c, ptw: etime.NegInf()}, nil
}

// Ingest judges (et, id): late events are dropped and counted, on-time
// events are accepted and advance ETW. All validation happens before any
// state change, so a rejected call leaves no trace.
func (e *Engine) Ingest(et int64, id string) (bool, error) {
	if id == "" {
		return false, ErrEmptyID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.clock.Ingest(et) {
		e.late++
		return false, nil
	}
	e.accepted = append(e.accepted, Event{Et: et, ID: id})
	return true, nil
}

// Heartbeat advances PTW to ts. It never touches maxEt, ETW, the accepted
// set or the late count. A ts below the current PTW is rejected.
func (e *Engine) Heartbeat(ts int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ts < e.ptw {
		return ErrHeartbeatRegression
	}
	e.ptw = ts
	return nil
}

// ETW returns the event-time watermark (negInf before any event).
func (e *Engine) ETW() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock.ETW()
}

// PTW returns the processing-time watermark (negInf before any heartbeat).
func (e *Engine) PTW() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ptw
}

// MaxEt returns the largest event time seen (negInf before any event).
func (e *Engine) MaxEt() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock.MaxEt()
}

// LateCount returns how many events have been dropped as late.
func (e *Engine) LateCount() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.late
}

// View returns all accepted events ordered by et, ties broken by id.
func (e *Engine) View() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, len(e.accepted))
	copy(out, e.accepted)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Et != out[j].Et {
			return out[i].Et < out[j].Et
		}
		return out[i].ID < out[j].ID
	})
	return out
}
