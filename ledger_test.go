package ledger

import (
	"errors"
	"sync"
	"testing"
)

func TestExampleRollbackAndRevive(t *testing.T) {
	l, err := New(1000)
	if err != nil {
		t.Fatal(err)
	}

	id1, err := l.Alloc(100)
	if err != nil || id1 != 1 {
		t.Fatalf("Alloc(100) = (%d, %v), want (1, nil)", id1, err)
	}
	if err := l.Snapshot("s1"); err != nil {
		t.Fatal(err)
	}
	id2, err := l.Alloc(50)
	if err != nil || id2 != 2 {
		t.Fatalf("Alloc(50) = (%d, %v), want (2, nil)", id2, err)
	}
	if err := l.Free(1); err != nil {
		t.Fatal(err)
	}
	if err := l.Snapshot("s2"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 150)
	assertUnique(t, l, "s1", 100)
	assertUnique(t, l, "s2", 0)
	assertReferenced(t, l, "s1", 100)
	assertReferenced(t, l, "s2", 50)

	id3, err := l.Alloc(30)
	if err != nil || id3 != 3 {
		t.Fatalf("Alloc(30) = (%d, %v), want (3, nil)", id3, err)
	}
	if err := l.Rollback("s1"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 100)
	assertUnique(t, l, "s1", 0)
	assertReferenced(t, l, "s1", 100)
	if err := l.Free(2); !errors.Is(err, ErrBlockDiscarded) {
		t.Fatalf("Free(2) error = %v, want ErrBlockDiscarded", err)
	}
	if err := l.Free(4); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("Free(4) error = %v, want ErrBlockNotFound", err)
	}
	if err := l.Free(1); err != nil {
		t.Fatal(err)
	}
	assertUnique(t, l, "s1", 100)
	if err := l.Snapshot("s2"); err != nil {
		t.Fatal(err)
	}
}

func TestFreeSnapshotOrderingAtSameTransaction(t *testing.T) {
	l, _ := New(100)
	id, _ := l.Alloc(10)

	if err := l.Free(id); err != nil {
		t.Fatal(err)
	}
	if err := l.Snapshot("dead-before"); err != nil {
		t.Fatal(err)
	}
	assertReferenced(t, l, "dead-before", 0)
	assertUsed(t, l, 0)

	l2, _ := New(100)
	id, _ = l2.Alloc(10)
	if err := l2.Snapshot("alive-at"); err != nil {
		t.Fatal(err)
	}
	if err := l2.Free(id); err != nil {
		t.Fatal(err)
	}
	assertReferenced(t, l2, "alive-at", 10)
	assertUsed(t, l2, 10)
}

func TestBirthAndDeathBoundaries(t *testing.T) {
	l, _ := New(1000)
	atBoundary, _ := l.Alloc(10)
	if err := l.Snapshot("base"); err != nil {
		t.Fatal(err)
	}
	afterBoundary, _ := l.Alloc(20)
	l.Free(atBoundary)
	if err := l.Snapshot("next"); err != nil {
		t.Fatal(err)
	}
	assertUnique(t, l, "base", 10)
	assertReferenced(t, l, "next", 20)
	assertUsed(t, l, 30)

	if err := l.Destroy("base"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 20)
	assertUnique(t, l, "next", 0)
	_ = afterBoundary
}

func TestHoldBlocksDestroyAndRollback(t *testing.T) {
	l, _ := New(1000)
	_, _ = l.Alloc(10)
	if err := l.Snapshot("base"); err != nil {
		t.Fatal(err)
	}
	if err := l.Hold("base"); err != nil {
		t.Fatal(err)
	}
	if err := l.Destroy("base"); !errors.Is(err, ErrHeld) {
		t.Fatalf("Destroy held = %v, want ErrHeld", err)
	}
	if _, err := l.Alloc(20); err != nil {
		t.Fatal(err)
	}
	if err := l.Snapshot("later"); err != nil {
		t.Fatal(err)
	}
	if err := l.Hold("later"); err != nil {
		t.Fatal(err)
	}
	if err := l.Rollback("base"); !errors.Is(err, ErrHeld) {
		t.Fatalf("Rollback over held = %v, want ErrHeld", err)
	}
	if err := l.Release("later"); err != nil {
		t.Fatal(err)
	}
	if err := l.Rollback("base"); err != nil {
		t.Fatal(err)
	}
	if err := l.Release("base"); err != nil {
		t.Fatal(err)
	}
	if err := l.Release("base"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("Release unheld = %v, want ErrNotHeld", err)
	}
}

func TestCapacityExactAndOverOne(t *testing.T) {
	l, _ := New(100)
	if _, err := l.Alloc(100); err != nil {
		t.Fatal(err)
	}
	_, err := l.Alloc(1)
	if !errors.Is(err, ErrOutOfSpace) {
		t.Fatalf("Alloc(1) = %v, want ErrOutOfSpace", err)
	}
	assertUsed(t, l, 100)
}

func TestDestroyIntermediateAndRebuildName(t *testing.T) {
	l, _ := New(1000)
	first, _ := l.Alloc(10)
	l.Snapshot("s1")
	second, _ := l.Alloc(20)
	l.Free(first)
	l.Snapshot("s2")
	third, _ := l.Alloc(30)
	l.Free(second)
	l.Snapshot("s3")

	assertUsed(t, l, 60)
	assertUnique(t, l, "s1", 10)
	assertUnique(t, l, "s2", 20)
	assertUnique(t, l, "s3", 0)

	if err := l.Destroy("s2"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 40)
	assertUnique(t, l, "s1", 10)
	assertUnique(t, l, "s3", 0)
	_ = third

	if err := l.Snapshot("s2"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Referenced("s2")
	if err != nil || got != 30 {
		t.Fatalf("rebuilt Referenced(s2)=(%d,%v), want 30", got, err)
	}

	if err := l.Destroy("s1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Destroy("s2"); err != nil {
		t.Fatal(err)
	}
	if err := l.Destroy("s3"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 30)
}

func TestRollbackNonLatestBirthBoundary(t *testing.T) {
	l, _ := New(1000)
	atOne, _ := l.Alloc(10)
	l.Snapshot("s1")
	atTwo, _ := l.Alloc(20)
	l.Free(atOne)
	l.Snapshot("s2")
	atThree, _ := l.Alloc(30)
	l.Free(atTwo)
	l.Snapshot("s3")

	if err := l.Rollback("s1"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 10)
	assertReferenced(t, l, "s1", 10)
	if err := l.Free(atOne); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{atTwo, atThree} {
		if err := l.Free(id); !errors.Is(err, ErrBlockDiscarded) {
			t.Fatalf("Free(%d)=%v, want ErrBlockDiscarded", id, err)
		}
	}
	assertUnique(t, l, "s1", 10)
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	l, _ := New(100)
	beforeCur := l.CurrentTxn()
	if _, err := l.Alloc(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := l.Alloc(101); !errors.Is(err, ErrOutOfSpace) {
		t.Fatal(err)
	}
	if err := l.Free(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := l.Snapshot(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := l.Destroy("missing"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatal(err)
	}
	if err := l.Release("missing"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatal(err)
	}
	if l.CurrentTxn() != beforeCur || l.Used() != 0 {
		t.Fatalf("state changed after rejected operations: cur=%d used=%d", l.CurrentTxn(), l.Used())
	}
}

func TestDeathBoundaryAtLaterSnapshot(t *testing.T) {
	l, _ := New(1000)
	id, _ := l.Alloc(10)
	l.Snapshot("first")
	l.Free(id)
	l.Snapshot("equal-death")
	assertReferenced(t, l, "first", 10)
	assertReferenced(t, l, "equal-death", 0)
	assertUnique(t, l, "first", 10)
}

func TestDestroyIntermediateTransfersUniqueAndReleasesBytes(t *testing.T) {
	l, _ := New(1000)
	shared, _ := l.Alloc(10)
	l.Snapshot("s1")
	exclusive, _ := l.Alloc(20)
	l.Snapshot("s2")
	if err := l.Free(shared); err != nil {
		t.Fatal(err)
	}
	if err := l.Free(exclusive); err != nil {
		t.Fatal(err)
	}
	l.Snapshot("s3")

	assertUsed(t, l, 30)
	assertUnique(t, l, "s1", 0)
	assertUnique(t, l, "s2", 20)
	if err := l.Destroy("s2"); err != nil {
		t.Fatal(err)
	}
	assertUsed(t, l, 10)
	assertUnique(t, l, "s1", 10)
}

func TestConcurrentOperations(t *testing.T) {
	l, _ := New(1 << 20)
	if err := l.Snapshot("base"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if id, err := l.Alloc(64); err == nil {
					_ = l.Free(id)
				}
				name := "snap"
				if err := l.Snapshot(name); err == nil {
					_ = l.Hold(name)
					_ = l.Release(name)
					_, _ = l.Referenced(name)
					_, _ = l.Unique(name)
					_ = l.Rollback("base")
					_ = l.Destroy(name)
				}
				_ = l.Hold("base")
				if err := l.Release("base"); err != nil && !errors.Is(err, ErrNotHeld) {
					t.Errorf("Release(base)=%v", err)
					return
				}
				_ = l.Used()
				_ = l.CurrentTxn()
			}
		}(worker)
	}
	wg.Wait()
	if l.Used() > l.Cap() {
		t.Fatalf("Used=%d exceeds Cap=%d", l.Used(), l.Cap())
	}
}

func assertUsed(t *testing.T, l *Ledger, want int64) {
	t.Helper()
	if got := l.Used(); got != want {
		t.Fatalf("Used() = %d, want %d", got, want)
	}
}

func assertReferenced(t *testing.T, l *Ledger, name string, want int64) {
	t.Helper()
	got, err := l.Referenced(name)
	if err != nil || got != want {
		t.Fatalf("Referenced(%q) = (%d, %v), want (%d, nil)", name, got, err, want)
	}
}

func assertUnique(t *testing.T, l *Ledger, name string, want int64) {
	t.Helper()
	got, err := l.Unique(name)
	if err != nil || got != want {
		t.Fatalf("Unique(%q) = (%d, %v), want (%d, nil)", name, got, err, want)
	}
}
