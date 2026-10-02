package scheduler

import "testing"

// TestSpecExample1 replays the first worked example from the spec:
// sleep-based bonus, wakeup preemption, interactive re-enqueue.
func TestSpecExample1(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0)) // sp=120, ts=100, prio=125
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 0})
	mustOK(t, s.Spawn(2, 0)) // equal prio: no preemption
	mustCurrent(t, s, 1)
	mustQueues(t, s, []PriorityQueue{pq(125, 2)}, nil)

	tickN(s, 100) // task 1 expires at now=100, s=0, not interactive
	if got := s.Now(); got != 100 {
		t.Fatalf("now=%d want 100", got)
	}
	mustCurrent(t, s, 2)
	if got := s.ExpiredTs(); got != 100 {
		t.Fatalf("expiredTs=%d want 100", got)
	}
	mustQueues(t, s, nil, []PriorityQueue{pq(125, 1)})

	tickN(s, 30) // now=130, task 2 ts_left=70
	if info, _ := s.State(2); info.TsLeft != 70 {
		t.Fatalf("task2 ts_left=%d want 70", info.TsLeft)
	}
	mustOK(t, s.Sleep()) // task 2 sleeps; swap makes task 1 current
	mustCurrent(t, s, 1)
	if got := s.ExpiredTs(); got != 0 {
		t.Fatalf("expiredTs=%d want 0 after swap", got)
	}

	tickN(s, 800) // now=930; task 1 cycles alone through swaps
	mustCurrent(t, s, 1)
	mustOK(t, s.Wake(2)) // s=min(1000,0+800)=800, bonus 8, prio=117
	mustCurrent(t, s, 2) // 117 < 125: preemption
	mustState(t, s, 2, TaskInfo{State: Running, Prio: 117, TsLeft: 70, S: 800})
	mustState(t, s, 1, TaskInfo{State: Queued, Prio: 125, TsLeft: 100, S: 0})
	mustQueues(t, s, []PriorityQueue{pq(125, 1)}, nil)

	tickN(s, 70)         // now=1000: task 2 expires with s=730, bonus 7
	mustCurrent(t, s, 2) // interactive: back to active, 118 < 125, keeps CPU
	mustState(t, s, 2, TaskInfo{State: Running, Prio: 118, TsLeft: 100, S: 730})

	tickN(s, 100) // now=1100: task 2 expires with s=630, bonus 6
	mustCurrent(t, s, 1)
	if got := s.ExpiredTs(); got != 1100 {
		t.Fatalf("expiredTs=%d want 1100", got)
	}
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 119, TsLeft: 100, S: 630})
	mustQueues(t, s, nil, []PriorityQueue{pq(119, 2)})
}

// TestSpecExample2 replays the second worked example: fork splitting and
// the t==1 immediate parent expiry with nr excluding the child.
func TestSpecExample2(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	tickN(s, 3) // now=3, task 1 ts_left=97
	mustOK(t, s.Fork(1, 2))
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 48, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 49, S: 0})
	mustQueues(t, s, []PriorityQueue{pq(125, 2)}, nil)

	tickN(s, 48) // now=51: task 1 expires, expired was empty
	mustCurrent(t, s, 2)
	if got := s.ExpiredTs(); got != 51 {
		t.Fatalf("expiredTs=%d want 51", got)
	}
	mustState(t, s, 2, TaskInfo{State: Running, Prio: 125, TsLeft: 49, S: 0})

	tickN(s, 48) // now=99: task 2 ts_left=1
	if info, _ := s.State(2); info.TsLeft != 1 {
		t.Fatalf("task2 ts_left=%d want 1", info.TsLeft)
	}
	mustOK(t, s.Fork(2, 3))
	// Parent (task 2) expired immediately with nr=2 (child not counted):
	// now-expiredTs=48 < 200, not starving, non-interactive -> expired
	// queue [1,2]; swap dispatches task 1; child 3 arrives without
	// preempting (equal prio 125).
	mustCurrent(t, s, 1)
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 100, S: 0})
	mustState(t, s, 3, TaskInfo{State: Queued, Prio: 125, TsLeft: 1, S: 0})
	mustQueues(t, s, []PriorityQueue{pq(125, 2, 3)}, nil)
	if got := s.ExpiredTs(); got != 0 {
		t.Fatalf("expiredTs=%d want 0 after swap", got)
	}
	if got := s.Now(); got != 99 {
		t.Fatalf("now=%d want 99 (fork does not advance the clock)", got)
	}
}
