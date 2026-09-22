package scheduler

import (
	"sync"
	"testing"
)

// recorder collects fired events in order.
type recorder struct {
	mu     sync.Mutex
	events []Event
	times  []int64
}

func (r *recorder) add(ev Event, now int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	r.times = append(r.times, now)
}

func (r *recorder) ids() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint64, len(r.events))
	for i, ev := range r.events {
		out[i] = ev.ID
	}
	return out
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recorder) timeAt(i int) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.times[i]
}

func newRecorderScheduler(cfg Config) (*Scheduler, *recorder) {
	r := &recorder{}
	var s *Scheduler
	cfg.OnFire = func(ev Event) { r.add(ev, s.Now()) }
	s, err := New(0, cfg)
	if err != nil {
		panic(err)
	}
	return s, r
}

func mustAdvance(t *testing.T, s *Scheduler, ticks int64) int {
	t.Helper()
	n, err := s.Advance(ticks)
	if err != nil {
		t.Fatalf("Advance(%d): %v", ticks, err)
	}
	return n
}

// TestFireTiming pins invariant 1: never early, fires exactly once at the
// first tick where cumulative advance >= delay.
func TestFireTiming(t *testing.T) {
	tests := []struct {
		name     string
		delay    int64
		steps    []int64 // Advance calls, in order
		wantStep int     // 1-based step during which it fires; 0 = never
		wantNow  int64   // injected time at firing
	}{
		{"delay 0 fires on first advance", 0, []int64{1}, 1, 1},
		{"delay 0 with big advance", 0, []int64{9}, 1, 1},
		{"delay 1 exact", 1, []int64{1}, 1, 1},
		{"delay 5 not early", 5, []int64{4, 1}, 2, 5},
		{"delay 5 exact single call", 5, []int64{5}, 1, 5},
		{"delay 5 overshoot fires at 5", 5, []int64{8}, 1, 5},
		{"delay 5 split steps", 5, []int64{2, 2, 2}, 3, 5},
		{"delay 64 crosses level boundary", 64, []int64{63, 1}, 2, 64},
		{"delay 100 one call", 100, []int64{100}, 1, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, r := newRecorderScheduler(Config{})
			if _, err := s.Add(tt.delay, nil); err != nil {
				t.Fatalf("Add: %v", err)
			}
			firedStep := 0
			for i, step := range tt.steps {
				before := r.count()
				mustAdvance(t, s, step)
				if r.count() > before && firedStep == 0 {
					firedStep = i + 1
				}
			}
			if tt.wantStep == 0 {
				if r.count() != 0 {
					t.Fatalf("fired %d times, want never", r.count())
				}
				return
			}
			if firedStep != tt.wantStep || r.timeAt(0) != tt.wantNow {
				t.Fatalf("fired at step %d now %d, want step %d now %d",
					firedStep, r.timeAt(0), tt.wantStep, tt.wantNow)
			}
			if r.count() != 1 {
				t.Fatalf("fired %d times, want exactly 1", r.count())
			}
			mustAdvance(t, s, 10) // must not fire again
			if r.count() != 1 {
				t.Fatalf("refired: %d events", r.count())
			}
		})
	}
}
