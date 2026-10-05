package labelpool

import "testing"

func TestAllocateMinOrderAndExhaustion(t *testing.T) {
	p := NewPool(16, 19, 10)
	for want := 16; want <= 19; want++ {
		got, ok := p.AllocateMin(0)
		if !ok || got != want {
			t.Fatalf("AllocateMin = %d,%v, want %d,true", got, ok, want)
		}
	}
	if _, ok := p.AllocateMin(0); ok {
		t.Fatal("expected exhaustion")
	}
	if p.WouldAvailable(0) {
		t.Fatal("WouldAvailable should be false when exhausted")
	}
	if p.Allocated() != 4 {
		t.Fatalf("Allocated = %d, want 4", p.Allocated())
	}
}

func TestIsolationBoundary(t *testing.T) {
	p := NewPool(16, 17, 50)
	if _, ok := p.AllocateMin(0); !ok { // 16
		t.Fatal("alloc 16 failed")
	}
	if _, ok := p.AllocateMin(0); !ok { // 17
		t.Fatal("alloc 17 failed")
	}
	p.Free(16, 10) // isolated until 60
	if until, ok := p.IsolatedUntil(16); !ok || until != 60 {
		t.Fatalf("IsolatedUntil = %d,%v, want 60,true", until, ok)
	}
	// At 59 the isolated label must not be handed out.
	if _, ok := p.AllocateMin(59); ok {
		t.Fatal("isolated label handed out at 59")
	}
	// At exactly 60 it becomes available and is the minimum.
	got, ok := p.AllocateMin(60)
	if !ok || got != 16 {
		t.Fatalf("AllocateMin(60) = %d,%v, want 16,true", got, ok)
	}
	if p.Promotions() != 1 || p.Examined() != 2 {
		t.Fatalf("probes = examined %d promotions %d, want 2/1", p.Examined(), p.Promotions())
	}
}

func TestMinAvailablePrefersSmallestFreed(t *testing.T) {
	p := NewPool(16, 20, 0)
	for i := 0; i < 3; i++ { // 16,17,18
		if _, ok := p.AllocateMin(0); !ok {
			t.Fatal("alloc failed")
		}
	}
	p.Free(17, 0) // hd=0: available again immediately
	got, ok := p.AllocateMin(0)
	if !ok || got != 17 {
		t.Fatalf("AllocateMin = %d,%v, want 17,true", got, ok)
	}
	got, ok = p.AllocateMin(0)
	if !ok || got != 19 {
		t.Fatalf("AllocateMin = %d,%v, want 19,true", got, ok)
	}
}

func TestAllocateSpecific(t *testing.T) {
	p := NewPool(16, 25, 100)
	// Never-allocated label out of the middle: gap labels become available.
	if !p.AllocateSpecific(20) {
		t.Fatal("AllocateSpecific(20) failed")
	}
	for _, want := range []int{16, 17, 18, 19, 21} {
		got, ok := p.AllocateMin(0)
		if !ok || got != want {
			t.Fatalf("AllocateMin = %d,%v, want %d,true", got, ok, want)
		}
	}
	// Isolated label retaken by affinity.
	p.Free(18, 10)
	if !p.AllocateSpecific(18) {
		t.Fatal("AllocateSpecific of isolated label failed")
	}
	if !p.IsAllocated(18) {
		t.Fatal("18 should be allocated")
	}
	// Free-heap label.
	p.Free(19, 0)
	if !p.AllocateSpecific(19) {
		t.Fatal("AllocateSpecific of free label failed")
	}
	// Already allocated and out of range.
	if p.AllocateSpecific(19) {
		t.Fatal("double allocation accepted")
	}
	if p.AllocateSpecific(26) || p.AllocateSpecific(15) {
		t.Fatal("out-of-range allocation accepted")
	}
}

func TestProbesIndependentOfAllocatedCount(t *testing.T) {
	for _, n := range []int{100, 5000} {
		p := NewPool(16, 16+n+10, 0)
		for i := 0; i < n; i++ {
			if _, ok := p.AllocateMin(0); !ok {
				t.Fatal("alloc failed")
			}
		}
		if _, ok := p.AllocateMin(0); !ok {
			t.Fatal("alloc failed")
		}
		if p.Examined() > 2 {
			t.Fatalf("n=%d: examined %d labels, want <= 2", n, p.Examined())
		}
	}
}
