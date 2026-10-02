package scheduler

import (
	"reflect"
	"testing"
)

// TestSpawnErrors checks spawn error precedence: invalid args, duplicate
// id, task limit (sleeping tasks included).
func TestSpawnErrors(t *testing.T) {
	s := New(2)
	mustErr(t, s.Spawn(-1, 0), ErrInvalidArgs)
	mustErr(t, s.Spawn(0, 20), ErrInvalidArgs)
	mustErr(t, s.Spawn(0, -21), ErrInvalidArgs)
	mustOK(t, s.Spawn(0, 0))
	mustErr(t, s.Spawn(0, 0), ErrDuplicateID)
	mustErr(t, s.Spawn(0, 19), ErrDuplicateID) // duplicate beats invalid-nice? no: nice 19 valid
	mustOK(t, s.Spawn(1, 0))
	mustErr(t, s.Spawn(2, 0), ErrFull)

	// Sleeping tasks count towards the limit.
	one := New(1)
	mustOK(t, one.Spawn(1, 0))
	mustOK(t, one.Sleep())
	mustErr(t, one.Spawn(2, 0), ErrFull)
}

// TestSpawnInvalidBeatsDuplicate: argument validation comes first.
func TestSpawnInvalidBeatsDuplicate(t *testing.T) {
	s := New(1)
	mustOK(t, s.Spawn(1, 0))
	mustErr(t, s.Spawn(1, 99), ErrInvalidArgs) // invalid nice, also duplicate
	mustErr(t, s.Spawn(-1, 99), ErrInvalidArgs)
}

// TestSleepWakeErrors checks the sleep/wake error cases and order.
func TestSleepWakeErrors(t *testing.T) {
	s := New(2)
	mustErr(t, s.Sleep(), ErrNoCurrent)
	mustErr(t, s.Wake(99), ErrNotFound)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Spawn(2, 0))
	mustErr(t, s.Wake(1), ErrNotSleeping) // running
	mustErr(t, s.Wake(2), ErrNotSleeping) // queued
	mustOK(t, s.Sleep())
	mustErr(t, s.Wake(3), ErrNotFound) // not-found beats not-sleeping
	mustOK(t, s.Wake(1))
}

// snapshotAll captures the full observable state of a scheduler.
func snapshotAll(s *Scheduler, ids ...int) interface{} {
	type snap struct {
		Now       int
		Cur       int
		HasCur    bool
		ExpiredTs int
		Queues    QueueSnapshot
		States    map[int]TaskInfo
	}
	cur, hasCur := s.Current()
	sn := snap{
		Now:       s.Now(),
		Cur:       cur,
		HasCur:    hasCur,
		ExpiredTs: s.ExpiredTs(),
		Queues:    s.Queues(),
		States:    map[int]TaskInfo{},
	}
	for _, id := range ids {
		if info, ok := s.State(id); ok {
			sn.States[id] = info
		}
	}
	return sn
}

// TestRejectedOpsChangeNothing: every rejected operation leaves the
// clock, tasks, queues, cur and expiredTs untouched.
func TestRejectedOpsChangeNothing(t *testing.T) {
	s := New(2)
	mustOK(t, s.Spawn(1, 0))
	mustOK(t, s.Spawn(2, 0))
	tickN(s, 10)
	mustOK(t, s.Sleep()) // task 1 sleeps, task 2 runs
	tickN(s, 5)

	rejected := []func() error{
		func() error { return s.Spawn(-1, 0) }, // invalid
		func() error { return s.Spawn(1, 0) },  // duplicate
		func() error { return s.Spawn(3, 0) },  // full
		func() error { return s.Wake(42) },     // not found
		func() error { return s.Wake(2) },      // not sleeping
		func() error { return s.Fork(-1, -1) }, // invalid
		func() error { return s.Fork(1, 5) },   // not current
		func() error { return s.Fork(2, 2) },   // not current (also dup)
		func() error { return s.Fork(2, 1) },   // not current (also dup)
	}
	for i, op := range rejected {
		before := snapshotAll(s, 1, 2)
		if err := op(); err == nil {
			t.Fatalf("op %d should have been rejected", i)
		}
		after := snapshotAll(s, 1, 2)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("op %d changed state: before=%+v after=%+v", i, before, after)
		}
	}

	// Sleep with no current task.
	idle := New(1)
	before := snapshotAll(idle)
	mustErr(t, idle.Sleep(), ErrNoCurrent)
	if !reflect.DeepEqual(before, snapshotAll(idle)) {
		t.Fatalf("rejected Sleep changed state")
	}
}
