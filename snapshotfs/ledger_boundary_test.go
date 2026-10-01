package snapshotfs

import (
	"errors"
	"testing"
)

// TestBirthAdjacentToSnapshot 出生号恰等于前一快照号（被前一快照包含，
// 故非独占）与大 1（独占）。
func TestBirthAdjacentToSnapshot(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(40)
	noErr(t, "Snapshot(prev)", l.Snapshot("prev"))
	_, _ = l.Alloc(60)
	noErr(t, "Snapshot(next)", l.Snapshot("next"))
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Free(2)", l.Free(2))
	if got := mustUnique(t, l, "prev"); got != 0 {
		t.Fatalf("Unique(prev) = %d, want 0 (b == t_prev, shared)", got)
	}
	if got := mustUnique(t, l, "next"); got != 60 {
		t.Fatalf("Unique(next) = %d, want 60 (b == t_prev+1, sole)", got)
	}
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100", got)
	}
}

// TestDeathAtOrBeforeLaterSnapshot 死亡号恰等于后一快照号时不被后者包含
// （t==d 排除）；死亡号小于后一快照号同样不含。
func TestDeathAtOrBeforeLaterSnapshot(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(a)", l.Snapshot("a"))
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Snapshot(b)", l.Snapshot("b"))
	if got := mustRef(t, l, "a"); got != 100 {
		t.Fatalf("Referenced(a) = %d, want 100", got)
	}
	if got := mustRef(t, l, "b"); got != 0 {
		t.Fatalf("Referenced(b) = %d, want 0 (t == d excluded)", got)
	}
	if got := mustUnique(t, l, "a"); got != 100 {
		t.Fatalf("Unique(a) = %d, want 100", got)
	}

	l2 := mustNew(t, 1000)
	_, _ = l2.Alloc(100)
	noErr(t, "l2 Snapshot(a)", l2.Snapshot("a"))
	noErr(t, "l2 Free(1)", l2.Free(1))
	noErr(t, "l2 Snapshot(b)", l2.Snapshot("b"))
	_, _ = l2.Alloc(10)
	noErr(t, "l2 Snapshot(c)", l2.Snapshot("c"))
	if got := mustRef(t, l2, "c"); got != 10 {
		t.Fatalf("Referenced(c) = %d, want 10", got)
	}
	if got := mustUnique(t, l2, "a"); got != 100 {
		t.Fatalf("Unique(a) = %d, want 100 (b,c do not contain)", got)
	}
}

// TestLatestSnapshotUnique 最新快照没有后继时的 Unique。
func TestLatestSnapshotUnique(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	if got := mustUnique(t, l, "s"); got != 0 {
		t.Fatalf("Unique(s) = %d, want 0 (block alive)", got)
	}
	_, _ = l.Alloc(50)
	noErr(t, "Free(2)", l.Free(2))
	if got := mustUnique(t, l, "s"); got != 0 {
		t.Fatalf("Unique(s) = %d, want 0", got)
	}
	noErr(t, "Free(1)", l.Free(1))
	if got := mustUnique(t, l, "s"); got != 100 {
		t.Fatalf("Unique(s) = %d, want 100", got)
	}
}

// TestDestroyMiddleSnapshot 销毁中间快照后相邻快照 Unique 增长、Used 下降。
func TestDestroyMiddleSnapshot(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))
	_, _ = l.Alloc(50)
	noErr(t, "Snapshot(s2)", l.Snapshot("s2"))
	_, _ = l.Alloc(20)
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Free(2)", l.Free(2))

	if got := l.Used(); got != 170 {
		t.Fatalf("Used = %d, want 170", got)
	}
	if got := mustUnique(t, l, "s2"); got != 50 {
		t.Fatalf("Unique(s2) = %d, want 50 before destroy", got)
	}
	noErr(t, "Destroy(s1)", l.Destroy("s1"))
	if got := mustUnique(t, l, "s2"); got != 150 {
		t.Fatalf("Unique(s2) = %d, want 150 after middle destroy", got)
	}
	if got := l.Used(); got != 170 {
		t.Fatalf("Used = %d, still 170 (s2 holds block1)", got)
	}
	noErr(t, "Destroy(s2)", l.Destroy("s2"))
	if got := l.Used(); got != 20 {
		t.Fatalf("Used = %d, want 20 (alive only)", got)
	}
}

// TestDestroyAllSnapshots 销毁全部快照后 Used 恰等于现存块字节数。
func TestDestroyAllSnapshots(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))
	_, _ = l.Alloc(50)
	noErr(t, "Snapshot(s2)", l.Snapshot("s2"))
	noErr(t, "Free(1)", l.Free(1))
	_, _ = l.Alloc(10)
	noErr(t, "Destroy(s1)", l.Destroy("s1"))
	noErr(t, "Destroy(s2)", l.Destroy("s2"))
	if got := l.Used(); got != 60 {
		t.Fatalf("Used = %d, want 60 (alive blocks only)", got)
	}
}

// TestAllocCapacityBoundary Alloc 恰达容量允许，超 1 拒绝且不消耗编号。
func TestAllocCapacityBoundary(t *testing.T) {
	l := mustNew(t, 100)
	_, err := l.Alloc(60)
	noErr(t, "Alloc(60)", err)
	_, err = l.Alloc(40)
	noErr(t, "Alloc(40) exact", err)
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100", got)
	}
	id, err := l.Alloc(1)
	wantErr(t, "Alloc(1) over by one", err, ErrOutOfSpace)
	if id != 0 {
		t.Fatalf("failed Alloc returned id %d, want 0", id)
	}
	noErr(t, "Free(2)", l.Free(2))
	id, err = l.Alloc(40)
	noErr(t, "Alloc(40) after free", err)
	if id != 3 {
		t.Fatalf("id after failed alloc = %d, want 3", id)
	}
}

// TestNewInvalid Cap 边界（1 与 2^50 含端点，之外拒绝）。
func TestNewInvalid(t *testing.T) {
	for _, cap := range []int64{0, -1, MaxCap + 1} {
		if _, err := New(cap); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d): got %v, want ErrInvalidArgument", cap, err)
		}
	}
	if _, err := New(MaxCap); err != nil {
		t.Fatalf("New(MaxCap): %v", err)
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1): %v", err)
	}
}

// TestRebuildAfterDestroy 销毁后同名重建，新快照采用当前事务号。
func TestRebuildAfterDestroy(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	noErr(t, "Destroy(s)", l.Destroy("s"))
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Snapshot(s) rebuilt", l.Snapshot("s"))
	if got := mustRef(t, l, "s"); got != 0 {
		t.Fatalf("Referenced(rebuilt s) = %d, want 0", got)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0", got)
	}
}

// TestRollbackBoundary Rollback 到非最新快照：b == t_name 保留，b == t_name+1 丢弃。
func TestRollbackBoundary(t *testing.T) {
	l := mustNew(t, 1000)
	id1, _ := l.Alloc(100)
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))
	id2, _ := l.Alloc(50)
	noErr(t, "Snapshot(s2)", l.Snapshot("s2"))
	id3, _ := l.Alloc(30)
	noErr(t, "Free(1)", l.Free(1))

	noErr(t, "Rollback(s1)", l.Rollback("s1"))
	if _, err := l.Referenced("s2"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("Referenced(s2) after rollback: %v", err)
	}
	noErr(t, "Free(id1) revived alive -> d=cur", l.Free(id1))
	wantErr(t, "Free(id1) dead now", l.Free(id1), ErrDead)
	wantErr(t, "Free(id2) discarded", l.Free(id2), ErrDiscarded)
	wantErr(t, "Free(id3) discarded", l.Free(id3), ErrDiscarded)
	wantErr(t, "Free(4) not found", l.Free(4), ErrNotFound)
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100", got)
	}
	if got := l.Cur(); got != 3 {
		t.Fatalf("Cur = %d, want 3 (unchanged)", got)
	}
}

// TestRollbackReviveThenFree Rollback 复活已死亡块后可再次 Free。
func TestRollbackReviveThenFree(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))
	_, _ = l.Alloc(50)
	noErr(t, "Free(1)", l.Free(1))
	if got := mustUnique(t, l, "s1"); got != 100 {
		t.Fatalf("Unique(s1) = %d, want 100", got)
	}
	noErr(t, "Rollback(s1)", l.Rollback("s1"))
	if got := mustUnique(t, l, "s1"); got != 0 {
		t.Fatalf("Unique(s1) revived = %d, want 0", got)
	}
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100", got)
	}
	noErr(t, "Free(1) again", l.Free(1))
	if got := mustUnique(t, l, "s1"); got != 100 {
		t.Fatalf("Unique(s1) = %d, want 100 after refree", got)
	}
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100 (s1 holds)", got)
	}
}
