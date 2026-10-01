package snapshotfs

import (
	"errors"
	"testing"
)

// TestHoldBlocksDestroyAndRollback 持有计数阻止 Destroy 与阻止 Rollback
// （name 自身被持有时仍可回滚）。
func TestHoldBlocksDestroyAndRollback(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))
	_, _ = l.Alloc(50)
	noErr(t, "Snapshot(s2)", l.Snapshot("s2"))

	wantErr(t, "Release unheld", l.Release("s1"), ErrNotHeld)
	noErr(t, "Hold(s1)", l.Hold("s1"))
	wantErr(t, "Destroy held", l.Destroy("s1"), ErrHeld)

	// name 自身被持有不阻止 Rollback；后继 s2 未被持有，故可回滚。
	noErr(t, "Rollback(s1) while self held", l.Rollback("s1"))
	if _, err := l.Referenced("s2"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("s2 should be destroyed by rollback: %v", err)
	}

	// 后继快照被持有时 Rollback 被拒绝，且状态完全不变。
	l2 := mustNew(t, 1000)
	_, _ = l2.Alloc(100)
	noErr(t, "l2 Snapshot(s1)", l2.Snapshot("s1"))
	_, _ = l2.Alloc(50)
	noErr(t, "l2 Snapshot(s2)", l2.Snapshot("s2"))
	noErr(t, "Hold(s2)", l2.Hold("s2"))
	wantErr(t, "Rollback blocked by later held", l2.Rollback("s1"), ErrHeld)
	if got := mustRef(t, l2, "s1"); got != 100 {
		t.Fatalf("Referenced(s1) = %d, want 100 (unchanged)", got)
	}
	if got := mustRef(t, l2, "s2"); got != 150 {
		t.Fatalf("Referenced(s2) = %d, want 150 (unchanged)", got)
	}
	noErr(t, "Release(s2) #1", l2.Release("s2"))
	wantErr(t, "Release(s2) #2", l2.Release("s2"), ErrNotHeld)
}

// TestDiscardedVsNotFound 已丢弃与不存在两类编号必须区分。
func TestDiscardedVsNotFound(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	_, _ = l.Alloc(50)
	noErr(t, "Rollback(s)", l.Rollback("s"))

	noErr(t, "Free(1) ok", l.Free(1))
	wantErr(t, "Free(1) dead now", l.Free(1), ErrDead)
	wantErr(t, "Free(2) discarded", l.Free(2), ErrDiscarded)
	wantErr(t, "Free(3) never allocated", l.Free(3), ErrNotFound)
}

// TestRejectionOrder 按规定顺序只报第一个错误，且被拒绝不改状态。
func TestRejectionOrder(t *testing.T) {
	l := mustNew(t, 100)

	// 参数非法优先于空间不足。
	if _, err := l.Alloc(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Alloc(0): %v", err)
	}
	if _, err := l.Alloc(MaxAlloc + 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Alloc(2^40+1): %v", err)
	}
	wantErr(t, "Snapshot empty name", l.Snapshot(""), ErrInvalidArgument)
	wantErr(t, "Free(0)", l.Free(0), ErrInvalidArgument)

	// 空间不足：不消耗编号、不改变 cur/Used。
	id, err := l.Alloc(80)
	noErr(t, "Alloc(80)", err)
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
	if got := l.Cur(); got != 1 {
		t.Fatalf("Cur = %d, want 1", got)
	}
	_, err = l.Alloc(21)
	wantErr(t, "Alloc(21) no space", err, ErrOutOfSpace)
	_, err = l.Alloc(21)
	wantErr(t, "Alloc(21) no space still", err, ErrOutOfSpace)

	// Free 顺序：不存在 > 已丢弃 > 已死亡（非法已先判）。
	wantErr(t, "Free(999)", l.Free(999), ErrNotFound)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	_, _ = l.Alloc(10)
	noErr(t, "Rollback discards id2", l.Rollback("s"))
	wantErr(t, "Free(2) discarded", l.Free(2), ErrDiscarded)
	noErr(t, "Free(1) once", l.Free(1))
	wantErr(t, "Free(1) dead", l.Free(1), ErrDead)

	// 快照操作顺序：重名 > 名字不存在（Destroy 被持有在后）。
	noErr(t, "Snapshot dup base", l.Snapshot("dup"))
	wantErr(t, "Snapshot dup", l.Snapshot("dup"), ErrSnapshotExists)
	for _, op := range []struct {
		name string
		fn   func(string) error
	}{
		{"Destroy", l.Destroy},
		{"Hold", l.Hold},
		{"Release", l.Release},
		{"Rollback", l.Rollback},
	} {
		if err := op.fn("ghost"); !errors.Is(err, ErrSnapshotMissing) {
			t.Fatalf("%s(ghost): %v", op.name, err)
		}
	}
	if _, err := l.Referenced("ghost"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("Referenced(ghost): %v", err)
	}
	if _, err := l.Unique("ghost"); !errors.Is(err, ErrSnapshotMissing) {
		t.Fatalf("Unique(ghost): %v", err)
	}

	// 被拒绝后已消耗编号仍连续：下一个 Alloc 编号为 3。
	next, err := l.Alloc(10)
	noErr(t, "Alloc next id", err)
	if next != 3 {
		t.Fatalf("next id = %d, want 3", next)
	}
}

// TestRejectedAllocNoStateChange 空间不足时 cur、Used、编号均不变。
func TestRejectedAllocNoStateChange(t *testing.T) {
	l := mustNew(t, 10)
	_, _ = l.Alloc(6)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	noErr(t, "Free(1)", l.Free(1))
	// 块1 被快照持有占 6；现存块字节 0，仍不可超分。
	_, err := l.Alloc(5)
	wantErr(t, "Alloc(5) over held space", err, ErrOutOfSpace)
	if got := l.Cur(); got != 2 {
		t.Fatalf("Cur = %d, want 2", got)
	}
	if got := l.Used(); got != 6 {
		t.Fatalf("Used = %d, want 6", got)
	}
	id, err := l.Alloc(4)
	noErr(t, "Alloc(4) fits after held accounting", err)
	if id != 2 {
		t.Fatalf("id = %d, want 2", id)
	}
}
