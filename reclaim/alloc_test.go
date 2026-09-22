package reclaim

import "testing"

func TestAcquireIsContiguous(t *testing.T) {
	a := New(3)
	want := []uint32{0, 1, 2}
	for _, w := range want {
		got, ok := a.Acquire()
		if !ok || got != w {
			t.Fatalf("acquire = (%d,%v), want %d", got, ok, w)
		}
	}
	if _, ok := a.Acquire(); ok {
		t.Fatal("capacity must reject further acquire")
	}
	if a.InUse() != 3 {
		t.Fatalf("in use = %d, want 3", a.InUse())
	}
}

func TestLowestFreeIndexReusedFirst(t *testing.T) {
	a := New(4)
	a.Acquire() // 0
	a.Acquire() // 1
	a.Acquire() // 2
	a.Release(2)
	a.Release(0)

	got, ok := a.Acquire()
	if !ok || got != 0 {
		t.Fatalf("reuse = (%d,%v), want lowest free 0", got, ok)
	}
	got, ok = a.Acquire()
	if !ok || got != 2 {
		t.Fatalf("reuse = (%d,%v), want next lowest 2", got, ok)
	}
	got, ok = a.Acquire()
	if !ok || got != 3 {
		t.Fatalf("acquire = (%d,%v), want fresh 3", got, ok)
	}
}

func TestReleaseBalancesInUse(t *testing.T) {
	a := New(2)
	idx, _ := a.Acquire()
	a.Release(idx)
	if a.InUse() != 0 {
		t.Fatalf("in use = %d, want 0", a.InUse())
	}
	if _, ok := a.Acquire(); !ok {
		t.Fatal("released index must be acquirable")
	}
}
