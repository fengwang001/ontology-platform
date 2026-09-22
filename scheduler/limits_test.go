package scheduler

import (
	"errors"
	"testing"
)

// TestParamErrors: invalid arguments are decidable errors and change nothing.
func TestParamErrors(t *testing.T) {
	tests := []struct {
		name string
		op   func(s *Scheduler) error
		want error
	}{
		{"negative delay", func(s *Scheduler) error { _, err := s.Add(-1, nil); return err }, ErrNegativeDelay},
		{"advance zero", func(s *Scheduler) error { _, err := s.Advance(0); return err }, ErrInvalidAdvance},
		{"advance negative", func(s *Scheduler) error { _, err := s.Advance(-7); return err }, ErrInvalidAdvance},
		{"reset negative delay", func(s *Scheduler) error {
			id, _ := s.Add(5, nil)
			return s.Reset(id, -2)
		}, ErrNegativeDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newRecorderScheduler(Config{})
			keep, _ := s.Add(2, nil)
			nowBefore := s.Now()
			if err := tt.op(s); !errors.Is(err, tt.want) {
				t.Fatalf("op error = %v, want %v", err, tt.want)
			}
			if s.Now() != nowBefore {
				t.Fatalf("clock moved on rejected op")
			}
			if err := s.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck after rejected op: %v", err)
			}
			mustAdvance(t, s, 2) // pre-existing timer still fires
			if r.count() != 1 || r.ids()[0] != keep {
				t.Fatalf("state corrupted by rejected op")
			}
		})
	}
}

// TestLimits: the three configurable limits reject immediately, leave no
// half-registered state, and the scheduler keeps working afterwards.
func TestLimits(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		run  func(t *testing.T, s *Scheduler)
	}{
		{"max advance", Config{MaxAdvance: 10}, func(t *testing.T, s *Scheduler) {
			if _, err := s.Advance(11); !errors.Is(err, ErrAdvanceTooLarge) {
				t.Fatalf("Advance(11) = %v", err)
			}
			if s.Now() != 0 {
				t.Fatalf("clock moved on rejected advance")
			}
		}},
		{"max timers", Config{MaxTimers: 2}, func(t *testing.T, s *Scheduler) {
			first, err := s.Add(1, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Add(1, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Add(1, nil); !errors.Is(err, ErrTooManyTimers) {
				t.Fatalf("third Add = %v, want ErrTooManyTimers", err)
			}
			if err := s.Cancel(first); err != nil { // free a slot: not a terminal state
				t.Fatalf("Cancel after rejection: %v", err)
			}
		}},
		{"max delay", Config{MaxDelay: 100}, func(t *testing.T, s *Scheduler) {
			if _, err := s.Add(101, nil); !errors.Is(err, ErrDelayTooLarge) {
				t.Fatalf("Add(101) = %v, want ErrDelayTooLarge", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newRecorderScheduler(tt.cfg)
			tt.run(t, s)
			if err := s.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck after rejection: %v", err)
			}
			// Not a terminal state: normal operation resumes.
			if _, err := s.Add(1, nil); err != nil {
				t.Fatalf("Add after rejection: %v", err)
			}
			mustAdvance(t, s, 1)
			if r.count() == 0 {
				t.Fatalf("scheduler dead after limit rejection")
			}
		})
	}
}

// TestSelfCheck exercises the self-check across representative states.
func TestSelfCheck(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, s *Scheduler)
	}{
		{"empty", func(t *testing.T, s *Scheduler) {}},
		{"after adds across levels", func(t *testing.T, s *Scheduler) {
			for _, d := range []int64{0, 1, 63, 64, 1000, 100000} {
				if _, err := s.Add(d, nil); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"mid schedule", func(t *testing.T, s *Scheduler) {
			for i := 0; i < 50; i++ {
				if _, err := s.Add(int64(i*3), nil); err != nil {
					t.Fatal(err)
				}
			}
			mustAdvance(t, s, 40)
		}},
		{"after cancels and resets", func(t *testing.T, s *Scheduler) {
			var ids []uint64
			for i := 0; i < 20; i++ {
				id, _ := s.Add(int64(i+1), nil)
				ids = append(ids, id)
			}
			for i := 0; i < 10; i += 2 {
				if err := s.Cancel(ids[i]); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Reset(ids[1], 100); err != nil {
				t.Fatal(err)
			}
			mustAdvance(t, s, 30)
		}},
		{"delay 0 pending", func(t *testing.T, s *Scheduler) {
			if _, err := s.Add(0, nil); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newRecorderScheduler(Config{})
			tt.run(t, s)
			if err := s.SelfCheck(); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
		})
	}
}
