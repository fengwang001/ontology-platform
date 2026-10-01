package snapshotfs

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, cap int64) *Ledger {
	t.Helper()
	l, err := New(cap)
	if err != nil {
		t.Fatalf("New(%d): %v", cap, err)
	}
	return l
}

func noErr(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", op, err)
	}
}

func wantErr(t *testing.T, op string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", op, got, want)
	}
}

func mustRef(t *testing.T, l *Ledger, name string) int64 {
	t.Helper()
	v, err := l.Referenced(name)
	if err != nil {
		t.Fatalf("Referenced(%s): %v", name, err)
	}
	return v
}

func mustUnique(t *testing.T, l *Ledger, name string) int64 {
	t.Helper()
	v, err := l.Unique(name)
	if err != nil {
		t.Fatalf("Unique(%s): %v", name, err)
	}
	return v
}

// TestWorkedExample 复现题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	l := mustNew(t, 1000)

	id1, err := l.Alloc(100)
	noErr(t, "Alloc(100)", err)
	if id1 != 1 {
		t.Fatalf("id1 = %d, want 1", id1)
	}
	noErr(t, "Snapshot(s1)", l.Snapshot("s1"))

	id2, err := l.Alloc(50)
	noErr(t, "Alloc(50)", err)
	if id2 != 2 {
		t.Fatalf("id2 = %d, want 2", id2)
	}
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Snapshot(s2)", l.Snapshot("s2"))

	if got := l.Used(); got != 150 {
		t.Fatalf("Used = %d, want 150", got)
	}
	if got := mustRef(t, l, "s1"); got != 100 {
		t.Fatalf("Referenced(s1) = %d, want 100", got)
	}
	if got := mustRef(t, l, "s2"); got != 50 {
		t.Fatalf("Referenced(s2) = %d, want 50", got)
	}
	if got := mustUnique(t, l, "s1"); got != 100 {
		t.Fatalf("Unique(s1) = %d, want 100", got)
	}
	if got := mustUnique(t, l, "s2"); got != 0 {
		t.Fatalf("Unique(s2) = %d, want 0", got)
	}

	id3, err := l.Alloc(30)
	noErr(t, "Alloc(30)", err)
	if id3 != 3 {
		t.Fatalf("id3 = %d, want 3", id3)
	}

	noErr(t, "Rollback(s1)", l.Rollback("s1"))
	if got := l.Used(); got != 100 {
		t.Fatalf("Used after rollback = %d, want 100", got)
	}
	if got := mustUnique(t, l, "s1"); got != 0 {
		t.Fatalf("Unique(s1) after rollback = %d, want 0", got)
	}
	if got := mustRef(t, l, "s1"); got != 100 {
		t.Fatalf("Referenced(s1) after rollback = %d, want 100", got)
	}

	wantErr(t, "Free(2) discarded", l.Free(2), ErrDiscarded)
	wantErr(t, "Free(4) not found", l.Free(4), ErrNotFound)
	noErr(t, "Free(1) revived-then-free", l.Free(1))
	if got := mustUnique(t, l, "s1"); got != 100 {
		t.Fatalf("Unique(s1) = %d, want 100", got)
	}
	noErr(t, "reuse name s2", l.Snapshot("s2"))
}

// TestFreeBeforeSnapshot 同一事务号内 Free 先于 Snapshot：
// 死亡号等于快照事务号，故该快照不包含此块。
func TestFreeBeforeSnapshot(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	if got := mustRef(t, l, "s"); got != 0 {
		t.Fatalf("Referenced(s) = %d, want 0 (d == t_s excluded)", got)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0", got)
	}
	noErr(t, "Destroy(s)", l.Destroy("s"))
	noErr(t, "Snapshot(s) reused", l.Snapshot("s"))
}

// TestFreeAfterSnapshot Snapshot 之后再 Free：cur 已加 1，
// 死亡号大于快照事务号，故快照仍包含该块。
func TestFreeAfterSnapshot(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	noErr(t, "Free(1)", l.Free(1))
	if got := mustRef(t, l, "s"); got != 100 {
		t.Fatalf("Referenced(s) = %d, want 100", got)
	}
	if got := mustUnique(t, l, "s"); got != 100 {
		t.Fatalf("Unique(s) = %d, want 100", got)
	}
	if got := l.Used(); got != 100 {
		t.Fatalf("Used = %d, want 100", got)
	}
	noErr(t, "Destroy(s)", l.Destroy("s"))
	if got := l.Used(); got != 0 {
		t.Fatalf("Used after destroy = %d, want 0", got)
	}
}

// TestBornAndFreedSameTransaction 块在当前事务号出生并同事务号释放，
// 随后建快照：空区间 [b,b) 不被任何快照包含，不占用空间。
func TestBornAndFreedSameTransaction(t *testing.T) {
	l := mustNew(t, 1000)
	_, _ = l.Alloc(100)
	noErr(t, "Free(1)", l.Free(1))
	noErr(t, "Snapshot(s)", l.Snapshot("s"))
	if got := l.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0", got)
	}
	if got := mustRef(t, l, "s"); got != 0 {
		t.Fatalf("Referenced = %d, want 0", got)
	}
}
