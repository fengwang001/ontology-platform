package scheduler

import "testing"

// TestSleepCounterDecrement: s decreases by 1 per running tick and never
// drops below 0.
func TestSleepCounterDecrement(t *testing.T) {
	s := New(1)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Sleep()) // sleeps at now=0
	tickN(s, 50)         // idle ticks
	mustOK(t, s.Wake(1))
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 50})
	tickN(s, 10)
	if info, _ := s.State(1); info.S != 40 {
		t.Fatalf("s=%d want 40", info.S)
	}
	tickN(s, 40) // s reaches 0 exactly
	if info, _ := s.State(1); info.S != 0 {
		t.Fatalf("s=%d want 0", info.S)
	}
	tickN(s, 10) // stays at 0
	if info, _ := s.State(1); info.S != 0 {
		t.Fatalf("s=%d want 0 (floor)", info.S)
	}
}

// TestSleepCap1000: a long sleep caps s at 1000.
func TestSleepCap1000(t *testing.T) {
	s := New(1)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Sleep())
	tickN(s, 1500)
	mustOK(t, s.Wake(1))
	if info, _ := s.State(1); info.S != 1000 {
		t.Fatalf("s=%d want 1000 (cap)", info.S)
	}
}

// TestWakeEmptyCurRunsDirectly: waking with an empty cur dispatches the
// task directly without touching any queue (no preemption involved).
func TestWakeEmptyCurRunsDirectly(t *testing.T) {
	s := New(2)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Sleep())
	tickN(s, 5)
	mustOK(t, s.Wake(1))
	mustCurrent(t, s, 1)
	mustQueues(t, s, nil, nil)
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 5})
}

// TestPreemptionEqualVsLess: equal prio does not preempt, prio smaller by
// one does; the preempted task returns to the head of its queue keeping
// its remaining timeslice.
func TestPreemptionEqualVsLess(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	tickN(s, 30)             // task 1 ts_left=70
	mustOK(t, s.Spawn(2, 0)) // equal prio 125: no preemption
	mustCurrent(t, s, 1)
	mustQueues(t, s, []PriorityQueue{pq(125, 2)}, nil)
	mustOK(t, s.Spawn(3, -1)) // prio 124 < 125: preempts
	mustCurrent(t, s, 3)
	// task 1 back to the head of queue 125 keeping ts_left=70.
	mustQueues(t, s, []PriorityQueue{pq(125, 1, 2)}, nil)
	mustState(t, s, 1, TaskInfo{State: Queued, Prio: 125, TsLeft: 70, S: 0})
	// When task 3 sleeps, task 1 resumes with its preserved slice.
	mustOK(t, s.Sleep())
	mustCurrent(t, s, 1)
	tickN(s, 1)
	if info, _ := s.State(1); info.TsLeft != 69 {
		t.Fatalf("ts_left=%d want 69 (preserved slice resumed)", info.TsLeft)
	}
}

// TestInteractiveExpiryBoundary: a task expiring with bonus exactly 7 is
// interactive and re-enters the active array; with bonus 6 it is not and
// enters the expired array.
func TestInteractiveExpiryBoundary(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Spawn(2, 0))
	mustOK(t, s.Sleep()) // task 1 sleeps at now=0; task 2 runs
	tickN(s, 800)        // task 2 cycles through expiries; now=800
	mustOK(t, s.Wake(1)) // s=800, prio=117, preempts task 2
	mustCurrent(t, s, 1)
	tickN(s, 100) // task 1 expires at now=900 with s=700, bonus 7
	// Interactive: re-entered the active array, never touched expired.
	mustCurrent(t, s, 1)
	if got := s.ExpiredTs(); got != 0 {
		t.Fatalf("expiredTs=%d want 0 (interactive task stayed active)", got)
	}
	if info, _ := s.State(1); info.Prio != 118 || info.S != 700 {
		t.Fatalf("state(1)=%+v want prio=118 s=700", info)
	}
	tickN(s, 100) // task 1 expires at now=1000 with s=600, bonus 6
	// Not interactive: entered the expired array, task 2 dispatched.
	mustCurrent(t, s, 2)
	if got := s.ExpiredTs(); got != 1000 {
		t.Fatalf("expiredTs=%d want 1000", got)
	}
	mustState(t, s, 1, TaskInfo{State: Queued, Prio: 119, TsLeft: 100, S: 600})
	mustQueues(t, s, nil, []PriorityQueue{pq(119, 1)})
}

// TestSwapClearsExpiredTs: when the active array is exhausted the arrays
// are swapped and expiredTs resets to 0.
func TestSwapClearsExpiredTs(t *testing.T) {
	s := New(1)
	mustOK(t, s.Spawn(1, 0))
	tickN(s, 100) // expires at now=100: goes to expired, then swap
	mustCurrent(t, s, 1)
	if got := s.ExpiredTs(); got != 0 {
		t.Fatalf("expiredTs=%d want 0 after swap", got)
	}
	mustQueues(t, s, nil, nil)
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 0})
}

// TestExpiredTsSetOnlyWhenEmpty: expiredTs is only assigned when the
// expired array was empty before the append.
func TestExpiredTsSetOnlyWhenEmpty(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Spawn(2, 0))
	mustOK(t, s.Spawn(3, 0))
	tickN(s, 100) // task 1 expires into an empty expired array
	if got := s.ExpiredTs(); got != 100 {
		t.Fatalf("expiredTs=%d want 100", got)
	}
	tickN(s, 100) // task 2 expires into a non-empty expired array
	if got := s.ExpiredTs(); got != 100 {
		t.Fatalf("expiredTs=%d want 100 (kept, array was non-empty)", got)
	}
	mustCurrent(t, s, 3)
	mustQueues(t, s, nil, []PriorityQueue{pq(125, 1, 2)})
}
