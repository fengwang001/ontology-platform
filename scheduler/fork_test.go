package scheduler

import "testing"

// TestForkSplitEven: t even, child gets ceil(t/2), parent keeps floor(t/2).
func TestForkSplitEven(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0)) // ts_left=100
	mustOK(t, s.Fork(1, 2))
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 50, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 50, S: 0})
}

// TestForkSplitOdd: t odd, child rounds up, parent rounds down.
func TestForkSplitOdd(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	tickN(s, 3) // ts_left=97
	mustOK(t, s.Fork(1, 2))
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 48, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 49, S: 0})
}

// TestForkSplitOne: t==1 makes the parent's share 0, so the parent
// expires immediately (full expiry flow without advancing now or
// changing s) before the child arrives.
func TestForkSplitOne(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	tickN(s, 99) // ts_left=1
	mustOK(t, s.Fork(1, 2))
	// Parent expired immediately: fresh slice, went to expired, swapped
	// back, running again. Child queued with ts_left=1, equal prio, no
	// preemption.
	mustCurrent(t, s, 1)
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 125, TsLeft: 100, S: 0})
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 125, TsLeft: 1, S: 0})
	mustQueues(t, s, []PriorityQueue{pq(125, 2)}, nil)
	if got := s.Now(); got != 99 {
		t.Fatalf("now=%d want 99 (fork does not advance the clock)", got)
	}
}

// TestForkChildSleepHalf: the child inherits floor(parent s / 2) and its
// prio derives from that; here the child does not preempt because its
// prio is worse than the (bonus-boosted) parent's.
func TestForkChildSleepHalf(t *testing.T) {
	s := New(3)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Sleep()) // sleeps at now=0
	tickN(s, 801)
	mustOK(t, s.Wake(1)) // s=801, prio=117
	tickN(s, 1)          // s=800, ts_left=99
	mustOK(t, s.Fork(1, 2))
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 121, TsLeft: 50, S: 400})
	mustState(t, s, 1, TaskInfo{State: Running, Prio: 117, TsLeft: 49, S: 800})
	mustCurrent(t, s, 1) // child prio 121 > parent's 117: no preemption
}

// TestForkErrors checks the fork error precedence: invalid args, no
// current task, id not current, duplicate id, task limit.
func TestForkErrors(t *testing.T) {
	s := New(3)
	mustErr(t, s.Fork(-1, 5), ErrInvalidArgs)
	mustErr(t, s.Fork(1, -5), ErrInvalidArgs)
	mustErr(t, s.Fork(1, 2), ErrNoCurrent)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Spawn(2, 0))
	mustErr(t, s.Fork(2, 5), ErrNotCurrent)  // 2 is not running
	mustErr(t, s.Fork(2, 2), ErrNotCurrent)  // not-current beats duplicate
	mustErr(t, s.Fork(1, 2), ErrDuplicateID) // 2 exists
	mustErr(t, s.Fork(1, 1), ErrDuplicateID) // 1 exists
	mustOK(t, s.Fork(1, 3))                  // fills the last slot
	mustErr(t, s.Fork(1, 4), ErrFull)

	full := New(1)
	mustOK(t, full.Spawn(1, 0))
	mustErr(t, full.Fork(1, 1), ErrDuplicateID) // duplicate beats full
	mustErr(t, full.Fork(1, 2), ErrFull)
}

// TestForkImmediateExpiryStarving builds a state where the t==1 parent
// expiry must evaluate starving with nr excluding the child: expired=[X]
// with now-expiredTs exactly 200, nr=2 without the child (starving,
// parent goes to expired despite being interactive) vs nr=3 with the
// child (would not starve). The parent landing in expired proves the
// child was not counted.
func TestForkImmediateExpiryStarving(t *testing.T) {
	s := New(8)
	mustOK(t, s.Spawn(2, -5))  // B: ts=500
	tickN(s, 299)              // B.ts_left=201
	mustOK(t, s.Sleep())       // B sleeps at now=299
	mustOK(t, s.Spawn(1, -20)) // X: prio 105, ts 800
	tickN(s, 900)              // B sleeps 900 ticks
	mustOK(t, s.Wake(2))       // B: s=900, prio 111, queued behind X
	// Tick until B is dispatched (X just expired into expired).
	for {
		if cur, _ := s.Current(); cur == 2 {
			break
		}
		s.Tick()
	}
	t0 := s.ExpiredTs()
	// Run B down to ts_left=1: exactly 200 ticks after t0.
	tickN(s, 200)
	info, _ := s.State(2)
	if info.TsLeft != 1 || info.S != 700 {
		t.Fatalf("scenario broken: B=%+v want ts_left=1 s=700", info)
	}
	if got := s.Now() - t0; got != 200 {
		t.Fatalf("scenario broken: now-t0=%d want 200", got)
	}
	// Fork with t=1: parent expires immediately. nr excludes the child
	// (nr=2), so starving is true (200 >= 200) and the interactive
	// parent enters the expired array instead of the active one.
	mustOK(t, s.Fork(2, 4))
	mustCurrent(t, s, 1) // X dispatched after the swap, not B
	mustState(t, s, 2, TaskInfo{State: Queued, Prio: 113, TsLeft: 500, S: 700})
	mustState(t, s, 4, TaskInfo{State: Queued, Prio: 117, TsLeft: 1, S: 350})
	t.Logf("判定依据: t=1 父任务立即到期, nr=2 不含子任务, now-expiredTs=200>=200 starving 为真, 交互父任务进入过期队列")
}
