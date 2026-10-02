package scheduler

import "testing"

func mustNew(t *testing.T, n int) *Scheduler {
	t.Helper()
	s, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	return s
}

func mustSpawn(t *testing.T, s *Scheduler, id, nice int) {
	t.Helper()
	if err := s.Spawn(id, nice); err != nil {
		t.Fatalf("Spawn(%d,%d): %v", id, nice, err)
	}
}

func mustTick(t *testing.T, s *Scheduler, times int) int {
	t.Helper()
	now := 0
	for range times {
		now = s.Tick()
	}
	return now
}

func requireCurrent(t *testing.T, s *Scheduler, want int) {
	t.Helper()
	got, ok := s.Current()
	if !ok || got != want {
		t.Fatalf("Current() = (%d,%v), want %d", got, ok, want)
	}
}

func requireTask(t *testing.T, s *Scheduler, id int, want TaskState) {
	t.Helper()
	got, err := s.State(id)
	if err != nil {
		t.Fatalf("State(%d): %v", id, err)
	}
	if got != want {
		t.Fatalf("State(%d) = %+v, want %+v", id, got, want)
	}
}

func taskByID(s *Scheduler, id int) *task {
	return s.tasks[id]
}

func TestFormulasAndClamping(t *testing.T) {
	cases := []struct{ sp, ts int }{
		{100, 800},
		{119, 420},
		{120, 100},
		{139, 5},
	}
	for _, tc := range cases {
		if got := TimeSlice(tc.sp); got != tc.ts {
			t.Fatalf("TimeSlice(%d)=%d, want %d", tc.sp, got, tc.ts)
		}
	}
	if Bonus(699) != 6 || Bonus(700) != 7 || Bonus(1000) != 10 {
		t.Fatal("bonus boundary failed")
	}
	if DynamicPrio(100, 1000) != 100 || DynamicPrio(139, 0) != 139 {
		t.Fatal("dynamic priority clamp failed")
	}
}

func TestSpecSleepWakePreemptionExample(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustSpawn(t, s, 2, 0)
	mustTick(t, s, 100)
	requireCurrent(t, s, 2)
	if s.ExpiredTs() != 100 {
		t.Fatalf("ExpiredTs()=%d, want 100", s.ExpiredTs())
	}

	mustTick(t, s, 30)
	if err := s.Sleep(); err != nil {
		t.Fatal(err)
	}
	requireCurrent(t, s, 1)
	if s.ExpiredTs() != 0 {
		t.Fatalf("ExpiredTs()=%d after swap, want 0", s.ExpiredTs())
	}

	mustTick(t, s, 800)
	if err := s.Wake(2); err != nil {
		t.Fatal(err)
	}
	requireCurrent(t, s, 2)
	requireTask(t, s, 2, TaskState{ID: 2, Nice: 0, SP: 120, State: StateRunning, Prio: 112, TsLeft: 70, Sleep: 800})

	mustTick(t, s, 70)
	requireCurrent(t, s, 2)
	requireTask(t, s, 2, TaskState{ID: 2, Nice: 0, SP: 120, State: StateRunning, Prio: 113, TsLeft: 100, Sleep: 730})

	mustTick(t, s, 100)
	requireCurrent(t, s, 1)
	if s.ExpiredTs() != 1100 {
		t.Fatalf("ExpiredTs()=%d, want 1100", s.ExpiredTs())
	}
}

func TestSpecForkExample(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustTick(t, s, 3)
	if err := s.Fork(1, 2); err != nil {
		t.Fatal(err)
	}
	requireTask(t, s, 1, TaskState{ID: 1, Nice: 0, SP: 120, State: StateRunning, Prio: 120, TsLeft: 48, Sleep: 0})
	requireTask(t, s, 2, TaskState{ID: 2, Nice: 0, SP: 120, State: StateQueued, Prio: 120, TsLeft: 49, Sleep: 0})

	mustTick(t, s, 48)
	requireCurrent(t, s, 2)
	mustTick(t, s, 48)
	if err := s.Fork(2, 3); err != nil {
		t.Fatal(err)
	}
	requireCurrent(t, s, 1)
	requireTask(t, s, 2, TaskState{ID: 2, Nice: 0, SP: 120, State: StateQueued, Prio: 120, TsLeft: 100, Sleep: 0})
	requireTask(t, s, 3, TaskState{ID: 3, Nice: 0, SP: 120, State: StateQueued, Prio: 120, TsLeft: 1, Sleep: 0})
	if got := s.Queues().Active[120]; len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("active prio 120 = %v, want [2 3]", got)
	}
}
