package scheduler

import (
	"errors"
	"testing"
)

func TestSleepClampAndWakeWhenIdleRuns(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustTick(t, s, 10)
	if err := s.Sleep(); err != nil {
		t.Fatal(err)
	}
	mustTick(t, s, 1200)
	if err := s.Wake(1); err != nil {
		t.Fatal(err)
	}
	requireCurrent(t, s, 1)
	requireTask(t, s, 1, TaskState{ID: 1, Nice: 0, SP: 120, State: StateRunning, Prio: 110, TsLeft: 90, Sleep: 1000})
}

func TestPreemptionKeepsFrontAndTimeSlice(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustSpawn(t, s, 2, 0)
	mustTick(t, s, 10)
	mustSpawn(t, s, 3, -1)
	requireCurrent(t, s, 3)
	requireTask(t, s, 1, TaskState{ID: 1, Nice: 0, SP: 120, State: StateQueued, Prio: 120, TsLeft: 90, Sleep: 0})
	if got := s.Queues().Active[120]; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("prio 120 queue = %v, want [1 2]", got)
	}
}

func TestInteractiveExpiryReturnsToActive(t *testing.T) {
	s := mustNew(t, 2)
	mustSpawn(t, s, 1, 0)
	mustSpawn(t, s, 2, 19)
	if err := s.Sleep(); err != nil {
		t.Fatal(err)
	}
	mustTick(t, s, 800)
	if err := s.Wake(1); err != nil {
		t.Fatal(err)
	}
	mustTick(t, s, 100)
	requireCurrent(t, s, 1)
	requireTask(t, s, 1, TaskState{ID: 1, Nice: 0, SP: 120, State: StateRunning, Prio: 113, TsLeft: 100, Sleep: 700})
	if len(s.Queues().Expired) != 0 {
		t.Fatalf("interactive task entered expired queues: %+v", s.Queues().Expired)
	}
}

func TestForkSplitsAndChildSleepHalves(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustTick(t, s, 48)
	if err := s.Fork(1, 2); err != nil {
		t.Fatal(err)
	}
	if got := taskByID(s, 1).tsLeft; got != 26 {
		t.Fatalf("even parent = %d, want 26", got)
	}
	if got := taskByID(s, 2).tsLeft; got != 26 {
		t.Fatalf("even child = %d, want 26", got)
	}
	if err := s.Fork(1, 3); err != nil {
		t.Fatal(err)
	}
	if got := taskByID(s, 1).tsLeft; got != 13 {
		t.Fatalf("odd parent = %d, want 13", got)
	}
	if got := taskByID(s, 3).tsLeft; got != 13 {
		t.Fatalf("odd child = %d, want 13", got)
	}
}

func TestForkOneTickImmediateExpiryAndNoChildInNr(t *testing.T) {
	s := mustNew(t, 3)
	mustSpawn(t, s, 1, 0)
	mustTick(t, s, 3)
	if err := s.Fork(1, 2); err != nil {
		t.Fatal(err)
	}
	mustTick(t, s, 48)
	mustTick(t, s, 48)
	if err := s.Fork(2, 3); err != nil {
		t.Fatal(err)
	}
	requireCurrent(t, s, 1)
	if got := s.Queues().Active[120]; len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("active queue = %v, want [2 3]", got)
	}
}

func TestForkErrorsInOrder(t *testing.T) {
	s := mustNew(t, 2)
	checks := []struct {
		id, child int
		want      error
	}{
		{-1, 1, ErrInvalidArgument},
		{1, -1, ErrInvalidArgument},
		{1, 2, ErrNoCurrentTask},
	}
	for _, check := range checks {
		if err := s.Fork(check.id, check.child); !errors.Is(err, check.want) {
			t.Fatalf("Fork(%d,%d)=%v, want %v", check.id, check.child, err, check.want)
		}
	}
	mustSpawn(t, s, 1, 0)
	if err := s.Fork(2, 3); !errors.Is(err, ErrCurrentTaskMismatch) {
		t.Fatalf("mismatch = %v", err)
	}
	mustSpawn(t, s, 2, 0)
	if err := s.Fork(1, 2); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := s.Fork(1, 3); !errors.Is(err, ErrSchedulerFull) {
		t.Fatalf("full = %v", err)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	s := mustNew(t, 1)
	if err := s.Spawn(-1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := s.Spawn(1, 20); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	mustSpawn(t, s, 1, 0)
	if err := s.Spawn(1, 0); !errors.Is(err, ErrTaskExists) {
		t.Fatal(err)
	}
	if err := s.Spawn(2, 0); !errors.Is(err, ErrSchedulerFull) {
		t.Fatal(err)
	}
	if err := s.Wake(2); !errors.Is(err, ErrTaskNotFound) {
		t.Fatal(err)
	}
	if err := s.Wake(1); !errors.Is(err, ErrTaskNotSleeping) {
		t.Fatal(err)
	}
	if err := s.Sleep(); err != nil {
		t.Fatal(err)
	}
	if err := s.Sleep(); !errors.Is(err, ErrNoCurrentTask) {
		t.Fatal(err)
	}
	if s.Now() != 0 || s.totalTasks != 1 {
		t.Fatalf("state changed: now=%d total=%d", s.Now(), s.totalTasks)
	}
	requireTask(t, s, 1, TaskState{ID: 1, Nice: 0, SP: 120, State: StateSleeping, Prio: 120, TsLeft: 100, Sleep: 0})
}

func starvationTask(id, sp, prio, tsLeft, sleepAmount int, state State) *task {
	return &task{id: id, sp: sp, nice: sp - 120, prio: prio, tsLeft: tsLeft, s: sleepAmount, state: state}
}

func TestStarvationBoundariesForNrTwoAndThree(t *testing.T) {
	cases := []struct {
		name    string
		nr      int
		elapsed int
	}{
		{"nr2-one-before", 2, 199},
		{"nr2-equal", 2, 200},
		{"nr3-one-before", 3, 299},
		{"nr3-equal", 3, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t, 3)
			s.now = 100
			s.cur = starvationTask(1, 120, 113, 1, 701, StateRunning)
			s.tasks[1] = s.cur
			s.totalTasks = tc.nr
			old := starvationTask(2, 139, 120, 5, 0, StateQueued)
			s.tasks[2] = old
			s.expired.pushBack(old)
			s.expiredTs = 100
			if tc.nr == 3 {
				waiting := starvationTask(3, 139, 138, 5, 0, StateQueued)
				s.tasks[3] = waiting
				s.active.pushBack(waiting)
			}
			s.now += tc.elapsed - 1
			starving := tc.elapsed == 200 || tc.elapsed == 300
			s.Tick()
			if starving {
				if tc.nr == 2 {
					requireCurrent(t, s, 1)
					if s.ExpiredTs() != 0 {
						t.Fatalf("ExpiredTs()=%d after required swap, want 0", s.ExpiredTs())
					}
				} else {
					requireCurrent(t, s, 3)
					if s.ExpiredTs() != 100 {
						t.Fatalf("ExpiredTs()=%d, want old expired timestamp 100", s.ExpiredTs())
					}
				}
				if !s.lastExpired {
					t.Fatal("starving interactive task was not routed to expired")
				}
			} else {
				if s.ExpiredTs() != 100 {
					t.Fatalf("ExpiredTs()=%d, want old expired timestamp 100", s.ExpiredTs())
				}
				requireCurrent(t, s, 1)
				if s.lastExpired {
					t.Fatal("non-starving interactive task was routed to expired")
				}
			}
		})
	}
}

func TestBitmapWordChecksWithTenThousandQueuedTasks(t *testing.T) {
	s := mustNew(t, 10_000)
	for id := range 10_000 {
		mustSpawn(t, s, id, 0)
	}
	s.ResetWordChecks()
	for range 100 {
		before := s.OperationWordChecks()
		s.Tick()
		if got := s.OperationWordChecks(); got > 4 {
			t.Fatalf("Tick checked %d words, want <=4", got)
		}
		_ = before
	}
	if got := s.WordChecks(); got > 400 {
		t.Fatalf("100 ticks checked %d words, want <=400", got)
	}
}
