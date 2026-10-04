package guard_test

import (
	"testing"

	"ontology/guard"
)

func TestLockScheduleAndCap(t *testing.T) {
	// M=2, Lk=100：第 2 次错误触发锁定。
	g := guard.New(2, 100)
	sn := []byte("A")

	locked, _, k := g.NoteConflict(sn, 30)
	if locked || k != 0 {
		t.Fatalf("first error: locked=%v k=%d", locked, k)
	}
	snap, _ := g.Get(sn)
	if snap.E != 1 {
		t.Fatalf("e=%d want 1", snap.E)
	}

	// 第 M 次仍只返回冲突信息，但同时锁定：k=1, lockUntil=40+100*2^0=140
	locked, lu, k := g.NoteConflict(sn, 40)
	if !locked || k != 1 || lu != 140 {
		t.Fatalf("second error: locked=%v lu=%d k=%d", locked, lu, k)
	}
	if snap, _ := g.Get(sn); snap.E != 0 {
		t.Fatalf("e should reset, got %d", snap.E)
	}
	if g.Locked(sn, 139) != true || g.Locked(sn, 140) != false {
		t.Fatal("lock boundary wrong")
	}

	// 再锁：k=2, 160+100*2^1=360
	locked, _, k = g.NoteConflict(sn, 150)
	if locked || k != 1 {
		t.Fatalf("unexpected")
	}
	locked, lu, k = g.NoteConflict(sn, 160)
	if !locked || k != 2 || lu != 360 {
		t.Fatalf("got locked=%v lu=%d k=%d", locked, lu, k)
	}

	// 继续成对触发：k=3..9 时长指数增长，k=10 起封顶 Lk*2^6=6400。
	now := int64(361)
	for kk := 3; kk <= 10; kk++ {
		if locked, _, _ := g.NoteConflict(sn, now); locked {
			t.Fatalf("k=%d first error unexpectedly locked", kk)
		}
		now++
		var l int64
		locked, l, _ = g.NoteConflict(sn, now)
		wantShift := uint(kk - 1)
		if kk > 7 {
			wantShift = 6
		}
		want := now + 100<<wantShift
		if !locked || l != want {
			t.Fatalf("k=%d locked=%v lu=%d want %d", kk, locked, l, want)
		}
		now = l + 1
	}
	snap, _ = g.Get(sn)
	if snap.K != 10 {
		t.Fatalf("final k=%d", snap.K)
	}
}

func TestResetKeepsK(t *testing.T) {
	g := guard.New(1, 7)
	sn := []byte("A")
	locked, lu, k := g.NoteConflict(sn, 10)
	if !locked || k != 1 || lu != 17 {
		t.Fatalf("got %v %d %d", locked, lu, k)
	}
	g.Reset(sn)
	snap, _ := g.Get(sn)
	if snap.E != 0 || snap.K != 1 || snap.LockUntil != 0 {
		t.Fatalf("after reset: %+v", snap)
	}
}
