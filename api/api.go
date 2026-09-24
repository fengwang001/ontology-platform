// Package api is the public face of the dual-watermark system.
package api

import (
	"errors"
	"fmt"

	"ontology/dual"
	"ontology/etime"
)

// Re-exported sentinel errors: distinguishable via errors.Is.
var (
	ErrNegativeLateness    = etime.ErrNegativeLateness
	ErrEmptyID             = dual.ErrEmptyID
	ErrHeartbeatRegression = dual.ErrHeartbeatRegression
)

// Event is one accepted event.
type Event = dual.Event

// System is the dual-watermark service. Safe for concurrent use.
type System struct {
	eng *dual.Engine
}

// New returns a System with allowed lateness l.
func New(lateness int64) (*System, error) {
	eng, err := dual.New(lateness)
	if err != nil {
		return nil, err
	}
	return &System{eng: eng}, nil
}

// Ingest reports whether the event was accepted (on time).
func (s *System) Ingest(et int64, id string) (bool, error) { return s.eng.Ingest(et, id) }

// Heartbeat advances the processing-time watermark only.
func (s *System) Heartbeat(ts int64) error { return s.eng.Heartbeat(ts) }

// ETW returns the event-time watermark.
func (s *System) ETW() int64 { return s.eng.ETW() }

// PTW returns the processing-time watermark.
func (s *System) PTW() int64 { return s.eng.PTW() }

// MaxEt returns the largest event time seen.
func (s *System) MaxEt() int64 { return s.eng.MaxEt() }

// LateCount returns the number of dropped late events.
func (s *System) LateCount() int64 { return s.eng.LateCount() }

// View returns accepted events ordered by et, ties by id.
func (s *System) View() []Event { return s.eng.View() }

// naive replays an op sequence with a plain from-scratch simulation:
// the reference every invariant-1 check is compared against.
func naive(l int64, ops [][2]int64, ids []string) (view []Event, late int64) {
	var maxEt int64
	seen := false
	for i, op := range ops {
		et, ts := op[0], op[1]
		if ts >= 0 { // heartbeat: must not affect the event side
			continue
		}
		if seen && et <= maxEt-l {
			late++
			continue
		}
		if !seen || et > maxEt {
			maxEt = et
		}
		seen = true
		view = append(view, Event{Et: et, ID: ids[i]})
	}
	return view, late
}

// SelfCheck verifies the four invariants on built-in op sequences.
func SelfCheck() error {
	if !etime.SelfCheck() {
		return errors.New("api: etime incremental-maintenance self-check failed")
	}
	// Invariants 1-3: interleaved sequence, compare with naive replay,
	// track monotonicity, and probe heartbeat independence.
	const l = 5
	ops := [][2]int64{{10, -1}, {0, 100}, {8, -1}, {20, -1}, {14, -1}, {0, 200}, {16, -1}, {15, -1}}
	ids := []string{"a", "", "b", "c", "d", "", "e", "f"}
	s, err := New(l)
	if err != nil {
		return err
	}
	lastETW, lastPTW := s.ETW(), s.PTW()
	for i, op := range ops {
		preETW, preView, preLate := s.ETW(), s.View(), s.LateCount()
		if op[1] >= 0 {
			if err := s.Heartbeat(op[1]); err != nil {
				return err
			}
			// Invariant 3: heartbeat leaves the event side untouched.
			if s.ETW() != preETW || s.LateCount() != preLate || fmt.Sprint(s.View()) != fmt.Sprint(preView) {
				return fmt.Errorf("api: heartbeat %d changed event-side state", op[1])
			}
		} else if _, err := s.Ingest(op[0], ids[i]); err != nil {
			return err
		}
		if s.ETW() < lastETW || s.PTW() < lastPTW { // invariant 3: monotonic
			return errors.New("api: watermark moved backwards")
		}
		lastETW, lastPTW = s.ETW(), s.PTW()
	}
	// Invariant 1: identical to naive replay, element by element.
	wantView, wantLate := naive(l, ops, ids)
	got := s.View()
	if s.LateCount() != wantLate || len(got) != len(wantView) {
		return errors.New("api: mismatch with naive replay")
	}
	for i := range got {
		if got[i] != wantView[i] {
			return fmt.Errorf("api: view[%d]=%v, want %v", i, got[i], wantView[i])
		}
	}
	// Invariant 2: PTW far ahead must not affect judgment.
	s2, _ := New(l)
	s2.Ingest(10, "a")
	s2.Heartbeat(1 << 40)
	if ok, _ := s2.Ingest(9, "b"); !ok { // 9 > ETW=5 though << PTW
		return errors.New("api: PTW influenced lateness judgment")
	}
	// Invariant 4: rejected ops leave no trace.
	s3, _ := New(l)
	s3.Ingest(10, "a")
	snap := fmt.Sprint(s3.ETW(), s3.PTW(), s3.MaxEt(), s3.LateCount(), s3.View())
	s3.Ingest(11, "")
	s3.Heartbeat(s3.PTW() - 1)
	if _, err := New(-1); !errors.Is(err, ErrNegativeLateness) {
		return errors.New("api: negative lateness not rejected")
	}
	if fmt.Sprint(s3.ETW(), s3.PTW(), s3.MaxEt(), s3.LateCount(), s3.View()) != snap {
		return errors.New("api: rejected operation changed state")
	}
	return nil
}
