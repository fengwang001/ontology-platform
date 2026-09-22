package scheduler

import (
	"fmt"
	"sort"

	"ontology/cascade"
	"ontology/timer"
)

// fireItem is a firing candidate: valid only while the timer is still
// Pending and its generation is unchanged (no Reset happened since).
type fireItem struct {
	t   *timer.Timer
	gen uint64
}

// Advance moves the injected clock forward by ticks (> 0) and fires every
// timer whose deadline is reached, in (tick, Add-order) sequence. It
// returns the number of timers fired.
func (s *Scheduler) Advance(ticks int64) (int, error) {
	if ticks <= 0 {
		return 0, ErrInvalidAdvance
	}
	if ticks > s.cfg.MaxAdvance {
		return 0, ErrAdvanceTooLarge
	}
	s.advMu.Lock()
	defer s.advMu.Unlock()
	s.touchedSlots, s.touchedTimers = 0, 0
	fired := 0
	for i := int64(0); i < ticks; i++ {
		fired += s.fireBatch(s.step())
	}
	return fired, nil
}

// step advances the clock by one tick and collects the firing candidates
// due at the new time, ordered by registration sequence.
func (s *Scheduler) step() []fireItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now++
	var items []fireItem
	for _, pe := range s.pending {
		if pe.t.State() == timer.Pending && pe.t.Gen() == pe.gen {
			items = append(items, fireItem{pe.t, pe.gen})
		}
	}
	s.pending = s.pending[:0]
	flushed, wrapped := s.wheels[0].AdvanceTo(s.now)
	s.touchedSlots++
	for _, e := range flushed {
		t := e.Value.(*timer.Timer)
		items = append(items, fireItem{t, t.Gen()})
		s.touchedTimers++
	}
	for l := 1; wrapped && l < len(s.wheels); l++ {
		flushed, wrapped = s.wheels[l].AdvanceTo(s.now)
		s.touchedSlots++
		for _, e := range flushed {
			t := e.Value.(*timer.Timer)
			s.touchedTimers++
			if cascade.Due(s.now, t.Deadline) {
				items = append(items, fireItem{t, t.Gen()})
			} else {
				s.touchedSlots++ // re-insertion touches the target slot
				_ = s.placeLocked(t)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].t.Seq < items[j].t.Seq })
	return items
}

// fireBatch fires the candidates still valid at their turn. The state and
// generation are re-checked under the lock right before each callback, so
// a Cancel or Reset landing after collection but before the callback
// suppresses the firing.
func (s *Scheduler) fireBatch(items []fireItem) int {
	fired := 0
	for _, it := range items {
		s.mu.Lock()
		ok := it.t.State() == timer.Pending && it.t.Gen() == it.gen
		var ev Event
		if ok {
			it.t.Fire()
			it.t.Detach()
			s.live--
			ev = Event{ID: it.t.ID, Seq: it.t.Seq, Deadline: it.t.Deadline, Payload: it.t.Payload}
		}
		s.mu.Unlock()
		if !ok {
			continue
		}
		fired++
		if s.cfg.OnFire != nil {
			s.cfg.OnFire(ev)
		}
	}
	return fired
}

// SelfCheck verifies the scheduler's internal invariants. It must be
// called while no Advance is running (it blocks on advMu otherwise).
func (s *Scheduler) SelfCheck() error {
	s.advMu.Lock()
	defer s.advMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	inWheels := 0
	for l, w := range s.wheels {
		if want := s.now - s.now%w.Tick; w.CurrentTime != want {
			return fmt.Errorf("level %d: currentTime %d, want %d (now=%d)", l, w.CurrentTime, want, s.now)
		}
		if want := w.SlotIndex(w.CurrentTime); w.Cursor() != want {
			return fmt.Errorf("level %d: cursor %d, want %d", l, w.Cursor(), want)
		}
		for i := 0; i < w.NumSlots(); i++ {
			for _, e := range w.Slot(i).Entries() {
				t, ok := e.Value.(*timer.Timer)
				if !ok || e.Handle != t.ID {
					return fmt.Errorf("level %d slot %d: corrupt entry", l, i)
				}
				if t.State() != timer.Pending {
					return fmt.Errorf("timer %d: state %d but still in a slot", t.ID, t.State())
				}
				if tl, ti := t.Location(); tl != l || ti != i {
					return fmt.Errorf("timer %d: location (%d,%d), found in (%d,%d)", t.ID, tl, ti, l, i)
				}
				inWheels++
			}
		}
	}
	validPending := 0
	for _, pe := range s.pending {
		if pe.t.State() == timer.Pending && pe.t.Gen() == pe.gen {
			validPending++
		}
	}
	pendingState := 0
	for _, t := range s.timers {
		if t.State() == timer.Pending {
			pendingState++
		}
	}
	if pendingState != inWheels+validPending {
		return fmt.Errorf("pending timers %d, but %d in wheels + %d pending list", pendingState, inWheels, validPending)
	}
	if s.live != pendingState {
		return fmt.Errorf("live count %d, pending timers %d", s.live, pendingState)
	}
	return nil
}
