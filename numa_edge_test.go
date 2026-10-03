package numa

import (
	"reflect"
	"testing"
)

func TestReaderQueuedAfterWriterAndGrantedByCancel(t *testing.T) {
	lock, err := New(Config{
		Nodes:      2,
		Threads:    4,
		NodeOf:     []int{0, 0, 0, 0},
		LocalLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.WLock(1))
	assertGranted(t, lock.RLock(2))
	assertGranted(t, lock.WUnlock(0), 2)
	assertGranted(t, lock.RLock(3))
	snapshot := lock.Snapshot()
	if !reflect.DeepEqual(snapshot.ReadQueue, []int{3}) {
		t.Fatalf("Qr=%v, want [3]", snapshot.ReadQueue)
	}

	assertGranted(t, lock.Cancel(1), 3)
	snapshot = lock.Snapshot()
	if snapshot.Phase != PhaseRead || !reflect.DeepEqual(snapshot.Readers, []int{2, 3}) {
		t.Fatalf("phase=%d readers=%v, want read phase with readers [2 3]", snapshot.Phase, snapshot.Readers)
	}
	assertInvariants(t, snapshot)
}

func TestLocalPassLimitAndEmptyGroup(t *testing.T) {
	lock, err := New(Config{
		Nodes:      2,
		Threads:    6,
		NodeOf:     []int{0, 0, 1, 0, 0, 1},
		LocalLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGranted(t, lock.WLock(0), 0)
	for _, thread := range []int{1, 2, 3, 4} {
		assertGranted(t, lock.WLock(thread))
	}

	assertGranted(t, lock.WUnlock(0), 1)
	assertPasses(t, lock, 1)
	assertGranted(t, lock.WUnlock(1), 3)
	assertPasses(t, lock, 2)
	assertGranted(t, lock.WUnlock(3), 2)
	snapshot := lock.Snapshot()
	if !reflect.DeepEqual(snapshot.GroupQueue, []int{0}) {
		t.Fatalf("G=%v, want node 0 queued behind granted remote node", snapshot.GroupQueue)
	}

	emptyGroupLock, err := New(Config{
		Nodes:      1,
		Threads:    3,
		NodeOf:     []int{0, 0, 0},
		LocalLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertGranted(t, emptyGroupLock.WLock(0), 0)
	assertGranted(t, emptyGroupLock.WLock(1))
	assertGranted(t, emptyGroupLock.WLock(2))
	assertGranted(t, emptyGroupLock.WUnlock(0), 1)
	assertGranted(t, emptyGroupLock.WUnlock(1), 2)
	assertPasses(t, emptyGroupLock, 2)
	assertInvariants(t, emptyGroupLock.Snapshot())
}

func TestReadersGrantedBeforeLocalPass(t *testing.T) {
	lock := newExampleLock(t)
	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.WLock(4))
	assertGranted(t, lock.RLock(1))
	assertGranted(t, lock.WUnlock(0), 1)
	snapshot := lock.Snapshot()
	if !reflect.DeepEqual(snapshot.GroupQueue, []int{0}) {
		t.Fatalf("G=%v, want [0]", snapshot.GroupQueue)
	}
	assertGranted(t, lock.RUnlock(1), 4)
	assertInvariants(t, lock.Snapshot())
}

func TestDowngradeGrantsReadQueue(t *testing.T) {
	lock := newExampleLock(t)
	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.WLock(4))
	assertGranted(t, lock.RLock(1))
	assertGranted(t, lock.Downgrade(0), 0, 1)
	snapshot := lock.Snapshot()
	if snapshot.Phase != PhaseRead || !reflect.DeepEqual(snapshot.Readers, []int{0, 1}) {
		t.Fatalf("phase=%d readers=%v, want readers [0 1]", snapshot.Phase, snapshot.Readers)
	}
	if !reflect.DeepEqual(snapshot.GroupQueue, []int{0}) {
		t.Fatalf("G=%v, want [0]", snapshot.GroupQueue)
	}
	assertGranted(t, lock.RUnlock(1))
	assertGranted(t, lock.RUnlock(0), 4)
	assertInvariants(t, lock.Snapshot())
}

func TestUpgradeConflictSuccessAndRemovesGroup(t *testing.T) {
	lock := newExampleLock(t)
	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.RLock(1))
	assertGranted(t, lock.WLock(4))
	assertGranted(t, lock.Downgrade(0), 0, 1)

	outcome := lock.Upgrade(0)
	if outcome.OK || outcome.Reason != ReasonUpgradeConflict {
		t.Fatalf("upgrade outcome=%+v, want conflict", outcome)
	}
	outcome = lock.Upgrade(1)
	if outcome.OK || outcome.Reason != ReasonUpgradeConflict {
		t.Fatalf("upgrade outcome=%+v, want conflict", outcome)
	}

	assertGranted(t, lock.RUnlock(1))
	assertGranted(t, lock.Upgrade(0), 0)
	snapshot := lock.Snapshot()
	if snapshot.Phase != PhaseWrite || snapshot.Writer != 0 {
		t.Fatalf("phase=%d writer=%d, want writer 0", snapshot.Phase, snapshot.Writer)
	}
	if len(snapshot.GroupQueue) != 0 || len(snapshot.WriteQueues[0]) != 1 || snapshot.WriteQueues[0][0] != 4 {
		t.Fatalf("G=%v Qw=%v, want empty G and local queue [4]", snapshot.GroupQueue, snapshot.WriteQueues)
	}
	assertGranted(t, lock.WUnlock(0), 4)
	assertInvariants(t, lock.Snapshot())
}

func TestCancelRemovesAndRequeuesNodeAtTail(t *testing.T) {
	lock, err := New(Config{
		Nodes:      3,
		Threads:    4,
		NodeOf:     []int{0, 1, 2, 1},
		LocalLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.WLock(1))
	assertGranted(t, lock.WLock(2))
	assertGranted(t, lock.Cancel(1))
	assertGranted(t, lock.WLock(3))
	snapshot := lock.Snapshot()
	if !reflect.DeepEqual(snapshot.GroupQueue, []int{2, 1}) {
		t.Fatalf("G=%v, want [2 1]", snapshot.GroupQueue)
	}
	assertInvariants(t, snapshot)
}

func TestInvalidConfigAndRejectionOrder(t *testing.T) {
	_, err := New(Config{Nodes: 0, Threads: 1, NodeOf: []int{0}, LocalLimit: 1})
	if err != ErrInvalidConfig {
		t.Fatalf("err=%v, want ErrInvalidConfig", err)
	}
	_, err = New(Config{Nodes: 1, Threads: 1, NodeOf: []int{1}, LocalLimit: 1})
	if err != ErrInvalidConfig {
		t.Fatalf("err=%v, want ErrInvalidConfig", err)
	}

	lock := newExampleLock(t)
	outcome := lock.RLock(6)
	if outcome.OK || outcome.Reason != ReasonThreadOutOfRange {
		t.Fatalf("outcome=%+v, want thread range error", outcome)
	}
	assertGranted(t, lock.WLock(0), 0)
	outcome = lock.Upgrade(1)
	if outcome.OK || outcome.Reason != ReasonInvalidState {
		t.Fatalf("outcome=%+v, want invalid state", outcome)
	}
	assertInvariants(t, lock.Snapshot())
}

func assertPasses(t *testing.T, lock *TeamRWMutex, want int) {
	t.Helper()
	if got := lock.Snapshot().Passes; got != want {
		t.Fatalf("p=%d, want %d", got, want)
	}
}

func assertInvariants(t *testing.T, snapshot Snapshot) {
	t.Helper()
	switch snapshot.Phase {
	case PhaseIdle:
		if snapshot.Writer != -1 || len(snapshot.Readers) != 0 {
			t.Fatalf("idle invariant failed: writer=%d readers=%v", snapshot.Writer, snapshot.Readers)
		}
		for node, queue := range snapshot.WriteQueues {
			if len(queue) != 0 {
				t.Fatalf("idle invariant failed: Qw[%d]=%v", node, queue)
			}
		}
		if len(snapshot.ReadQueue) != 0 || len(snapshot.GroupQueue) != 0 {
			t.Fatalf("idle queues not empty: Qr=%v G=%v", snapshot.ReadQueue, snapshot.GroupQueue)
		}
	case PhaseRead:
		if len(snapshot.Readers) == 0 {
			t.Fatal("read phase has no readers")
		}
	case PhaseWrite:
		if snapshot.Writer < 0 || len(snapshot.Readers) != 0 {
			t.Fatalf("write invariant failed: writer=%d readers=%v", snapshot.Writer, snapshot.Readers)
		}
	}
}
