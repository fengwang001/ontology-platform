package scheduler

import (
	"errors"
	"testing"
)

// TestCancelInFlight pins invariant 4: a timer already collected for firing
// but whose callback has not run yet must not fire if cancelled (or reset)
// in between.
func TestCancelInFlight(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(s *Scheduler, id uint64) // runs inside first callback
		wantFired int                           // total fired after all advances
		check     func(t *testing.T, s *Scheduler, id uint64)
	}{
		{"cancel from sibling callback", func(s *Scheduler, id uint64) {
			if err := s.Cancel(id); err != nil {
				t.Errorf("Cancel in flight: %v", err)
			}
		}, 1, func(t *testing.T, s *Scheduler, id uint64) {
			if err := s.Cancel(id); !errors.Is(err, ErrTimerCancelled) {
				t.Errorf("post Cancel = %v, want ErrTimerCancelled", err)
			}
		}},
		{"reset from sibling callback", func(s *Scheduler, id uint64) {
			if err := s.Reset(id, 10); err != nil {
				t.Errorf("Reset in flight: %v", err)
			}
		}, 2, func(t *testing.T, s *Scheduler, id uint64) {}},
		{"cancel before advance", nil, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s *Scheduler
			var target uint64
			r := &recorder{}
			cfg := Config{OnFire: func(ev Event) {
				r.add(ev, s.Now())
				if tt.mutate != nil && ev.ID != target {
					tt.mutate(s, target)
				}
			}}
			s, _ = New(0, cfg)
			if _, err := s.Add(2, nil); err != nil { // fires first (lower seq)
				t.Fatal(err)
			}
			var err error
			target, err = s.Add(2, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.name == "cancel before advance" {
				if err := s.Cancel(target); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
			}
			mustAdvance(t, s, 2)
			if tt.name == "reset from sibling callback" {
				mustAdvance(t, s, 10) // let the reset timer fire
			}
			if r.count() != tt.wantFired {
				t.Fatalf("fired %d, want %d", r.count(), tt.wantFired)
			}
			for _, id := range r.ids() {
				if id == target && tt.name != "reset from sibling callback" {
					t.Fatalf("cancelled timer %d fired", target)
				}
			}
			if tt.check != nil {
				tt.check(t, s, target)
			}
			if err := s.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
		})
	}
}

// TestHandleIdempotency pins invariant 5: repeated/late handle operations
// give distinct, decidable results and never corrupt other timers.
func TestHandleIdempotency(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, s *Scheduler)
	}{
		{"double cancel", func(t *testing.T, s *Scheduler) {
			id, _ := s.Add(5, nil)
			if err := s.Cancel(id); err != nil {
				t.Fatalf("first Cancel: %v", err)
			}
			if err := s.Cancel(id); !errors.Is(err, ErrTimerCancelled) {
				t.Fatalf("second Cancel = %v, want ErrTimerCancelled", err)
			}
		}},
		{"cancel fired", func(t *testing.T, s *Scheduler) {
			id, _ := s.Add(1, nil)
			mustAdvance(t, s, 1)
			if err := s.Cancel(id); !errors.Is(err, ErrTimerFired) {
				t.Fatalf("Cancel fired = %v, want ErrTimerFired", err)
			}
		}},
		{"reset cancelled", func(t *testing.T, s *Scheduler) {
			id, _ := s.Add(5, nil)
			if err := s.Cancel(id); err != nil {
				t.Fatal(err)
			}
			if err := s.Reset(id, 3); !errors.Is(err, ErrTimerCancelled) {
				t.Fatalf("Reset cancelled = %v, want ErrTimerCancelled", err)
			}
		}},
		{"reset fired", func(t *testing.T, s *Scheduler) {
			id, _ := s.Add(1, nil)
			mustAdvance(t, s, 1)
			if err := s.Reset(id, 3); !errors.Is(err, ErrTimerFired) {
				t.Fatalf("Reset fired = %v, want ErrTimerFired", err)
			}
		}},
		{"unknown handle", func(t *testing.T, s *Scheduler) {
			if err := s.Cancel(999); !errors.Is(err, ErrTimerNotFound) {
				t.Fatalf("Cancel unknown = %v", err)
			}
			if err := s.Reset(999, 1); !errors.Is(err, ErrTimerNotFound) {
				t.Fatalf("Reset unknown = %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newRecorderScheduler(Config{})
			other, _ := s.Add(3, nil) // must survive every scenario
			tt.run(t, s)
			mustAdvance(t, s, 3)
			found := false
			for _, id := range r.ids() {
				if id == other {
					found = true
				}
			}
			if !found {
				t.Fatalf("unrelated timer %d did not fire", other)
			}
			if err := s.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
		})
	}
}
