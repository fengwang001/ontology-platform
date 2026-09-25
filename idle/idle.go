// Package idle holds the processing-time state of one event stream:
// the current watermark, the last event's processing time, the count
// of late (dropped) events, and the advance rules. It depends only on
// package wtm. Input validation is the api package's job; Stream
// assumes non-empty keys and non-negative ts/pt.
package idle

import (
	"sync"

	"ontology/wtm"
)

// Stream is the mutable watermark state. All fields are unexported.
type Stream struct {
	mu sync.Mutex

	delay   int64
	timeout int64

	wm     int64 // current watermark, only moves forward
	lastPT int64 // processing time of the most recent Feed; wtm.NegInf before any

	dropped int                 // late events discarded
	seen    map[string]struct{} // accepted data events, recorded by Key

	// lateChecks counts recorded events examined while deciding if an
	// incoming event is late. Lateness is decided solely by comparing
	// ts-delay with wm, so no history is ever examined and this stays
	// 0; a history-scanning implementation would make it grow. It has
	// no exported accessor by design.
	lateChecks int
}

// New creates a stream. delay and timeout must be positive (caller
// enforces); watermark and lastPT start at negative infinity.
func New(delay, timeout int64) *Stream {
	return &Stream{
		delay:   delay,
		timeout: timeout,
		wm:      wtm.NegInf,
		lastPT:  wtm.NegInf,
		seen:    make(map[string]struct{}),
	}
}

// Feed applies one data event at processing time pt. Per the rules it
// first stamps lastEventPT = pt, then a strictly late event is
// dropped without touching the watermark; otherwise the watermark
// advances by max and the event is recorded.
func (s *Stream) Feed(key string, ts, pt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastPT = pt
	mark := wtm.Mark(ts, s.delay)

	// Decide lateness against the single current watermark. No event
	// in history is examined, hence lateChecks is not incremented.
	if wtm.Late(mark, s.wm) {
		s.dropped++
		return
	}

	s.wm = wtm.Advance(s.wm, mark)
	s.seen[key] = struct{}{}
}

// Tick advances the processing-time clock without an event. When the
// gap since the last event reaches timeout the watermark is pushed to
// pt-delay (by max); otherwise nothing happens.
func (s *Stream) Tick(pt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if wtm.Idle(pt, s.lastPT, s.timeout) {
		s.wm = wtm.Advance(s.wm, wtm.Mark(pt, s.delay))
	}
}

// Watermark returns the current watermark (math.MinInt64 before any
// contribution).
func (s *Stream) Watermark() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wm
}

// Dropped returns the number of late events discarded.
func (s *Stream) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}
