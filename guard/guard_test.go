package guard

import (
	"errors"
	"testing"
)

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		m  int
		lk int64
	}{
		{0, 100}, {11, 100}, {2, 0}, {2, 1_000_001}, {-1, 100},
	}
	for i, c := range cases {
		if _, err := New(c.m, c.lk); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
}

func TestNoteAndLock(t *testing.T) {
	g, _ := New(2, 100)
	sn := Sn("A")

	// 第 1 次错误：e=1，未锁定。
	e, locked := g.Note(sn, 30)
	if locked || e.E != 1 || e.K != 0 {
		t.Fatalf("first conflict: %+v locked=%v", e, locked)
	}
	if g.Locked(sn, 31) {
		t.Fatal("must not be locked before threshold")
	}

	// 第 M=2 次错误仍计入并立即锁定：k=1, lockUntil=40+100*2^0=140。
	e, locked = g.Note(sn, 40)
	if !locked || e.K != 1 || e.E != 0 || e.LockUntil != 140 {
		t.Fatalf("threshold conflict: %+v locked=%v", e, locked)
	}
	if !g.Locked(sn, 139) {
		t.Fatal("now=139 must be locked")
	}
	if g.Locked(sn, 140) {
		t.Fatal("now==lockUntil must be unlocked")
	}

	// 锁定期后的两次新错误：k=2, lockUntil=160+100*2^1=360。
	g.Note(sn, 150)
	e, locked = g.Note(sn, 160)
	if !locked || e.K != 2 || e.LockUntil != 360 {
		t.Fatalf("second lock: %+v", e)
	}
}

func TestBackoffCap(t *testing.T) {
	g, _ := New(1, 100)
	sn := Sn("A")
	var e Entry
	// M=1：每次错误都触发锁定。k=10 时时长封顶 100*2^6=6400。
	for i := 1; i <= 9; i++ {
		e, _ = g.Note(sn, int64(i*1000))
	}
	if e.K != 9 {
		t.Fatalf("pre cap k=%d", e.K)
	}
	e, _ = g.Note(sn, 10_000)
	if e.K != 10 || e.LockUntil != 10_000+6400 {
		t.Fatalf("capped lock: %+v", e)
	}
	// 更大 k 仍停留在 2^6 封顶。
	e, _ = g.Note(sn, 20_000)
	if e.K != 11 || e.LockUntil != 20_000+6400 {
		t.Fatalf("cap holds: %+v", e)
	}
}

func TestClearKeepsK(t *testing.T) {
	g, _ := New(2, 100)
	sn := Sn("A")
	g.Note(sn, 1)
	g.Note(sn, 2) // k=1, locked until 102
	if g.Peek(sn).K != 1 || !g.Locked(sn, 50) {
		t.Fatal("setup failed")
	}
	g.Clear(sn)
	p := g.Peek(sn)
	if p.K != 1 || p.E != 0 || p.LockUntil != 0 {
		t.Fatalf("clear must keep k: %+v", p)
	}
	if g.Locked(sn, 50) {
		t.Fatal("clear must release lock")
	}
}
