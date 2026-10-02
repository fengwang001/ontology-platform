package scheduler

import (
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("error: got %v want %v", got, want)
	}
}

func tickN(s *Scheduler, n int) {
	for i := 0; i < n; i++ {
		s.Tick()
	}
}

func mustCurrent(t *testing.T, s *Scheduler, want int) {
	t.Helper()
	got, ok := s.Current()
	if !ok || got != want {
		t.Fatalf("current: got (%d,%v) want %d", got, ok, want)
	}
}

func mustState(t *testing.T, s *Scheduler, id int, want TaskInfo) {
	t.Helper()
	got, ok := s.State(id)
	if !ok {
		t.Fatalf("state(%d): not found", id)
	}
	if got != want {
		t.Fatalf("state(%d): got %+v want %+v", id, got, want)
	}
}

func mustQueues(t *testing.T, s *Scheduler, active, expired []PriorityQueue) {
	t.Helper()
	got := s.Queues()
	if !equalQueues(got.Active, active) || !equalQueues(got.Expired, expired) {
		t.Fatalf("queues: got %+v want active=%+v expired=%+v", got, active, expired)
	}
}

func pq(prio int, ids ...int) PriorityQueue {
	return PriorityQueue{Prio: prio, IDs: ids}
}

// TestTimeslice covers the timeslice function including sp exactly 100,
// 120 and 139.
func TestTimeslice(t *testing.T) {
	cases := []struct{ sp, want int }{
		{100, 800}, {110, 600}, {119, 420}, {120, 100}, {121, 95}, {130, 50}, {139, 5},
	}
	for _, c := range cases {
		if got := Timeslice(c.sp); got != c.want {
			t.Errorf("Timeslice(%d)=%d want %d", c.sp, got, c.want)
		}
	}
}

// TestTimesliceViaSpawn checks ts_left for nice values mapping to sp
// 100, 120 and 139.
func TestTimesliceViaSpawn(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, -20)) // sp=100
	mustOK(t, s.Spawn(2, 0))   // sp=120
	mustOK(t, s.Spawn(3, 19))  // sp=139
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 105, TsLeft: 800, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 100, S: 0})
	mustState(t, s, 3, TaskInfo{State: Queued, Prio: 139, TsLeft: 5, S: 0})
}

// TestPrioFormula covers the dynamic priority formula including the 100
// and 139 clamps and the bonus 7/6 interactive boundary.
func TestPrioFormula(t *testing.T) {
	cases := []struct{ sp, s, want int }{
		{120, 0, 125},
		{120, 700, 118},  // bonus 7
		{120, 600, 119},  // bonus 6
		{100, 1000, 100}, // clamped from 95
		{139, 0, 139},    // clamped from 144
		{100, 0, 105},
		{139, 1000, 134},
	}
	for _, c := range cases {
		if got := Prio(c.sp, c.s); got != c.want {
			t.Errorf("Prio(%d,%d)=%d want %d", c.sp, c.s, got, c.want)
		}
	}
	if !Interactive(700) || Interactive(699) {
		t.Errorf("interactive boundary wrong")
	}
}

// TestPrioClampViaTasks observes the clamps through task state.
func TestPrioClampViaTasks(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 19))  // sp=139, prio clamped to 139
	mustOK(t, s.Spawn(2, -20)) // sp=100, prio 105 preempts
	mustState(t, s, 1, TaskInfo{State: Queued, Prio: 139, TsLeft: 5, S: 0})
	mustCurrent(t, s, 2)
	// Let task 2 sleep 1200 ticks: s caps at 1000, prio clamps to 100.
	mustOK(t, s.Sleep())
	tickN(s, 1200)
	mustOK(t, s.Wake(2))
	info, _ := s.State(2)
	if info.S != 1000 || info.Prio != 100 {
		t.Fatalf("state(2): got %+v want s=1000 prio=100", info)
	}
}
