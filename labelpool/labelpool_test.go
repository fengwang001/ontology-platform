package labelpool

import (
	"fmt"
	"testing"
)

func mustPool(t *testing.T, lo, hi int, hd int64) *Pool {
	t.Helper()
	p, err := New(lo, hi, hd)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", lo, hi, hd, err)
	}
	return p
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		lo, hi int
		hd     int64
		ok     bool
	}{
		{16, 16, 0, true},
		{16, 1048575, 1_000_000_000, true},
		{15, 100, 0, false},
		{16, 1048576, 0, false},
		{200, 100, 0, false},
		{16, 100, -1, false},
		{16, 100, 1_000_000_001, false},
	}
	for _, c := range cases {
		_, err := New(c.lo, c.hi, c.hd)
		if got := err == nil; got != c.ok {
			t.Errorf("New(%d,%d,%d) ok=%v, want %v", c.lo, c.hi, c.hd, got, c.ok)
		}
	}
}

func TestAllocMinAndExhaustion(t *testing.T) {
	p := mustPool(t, 100, 103, 50)
	for want := 100; want <= 103; want++ {
		got, ok := p.Alloc(fmt.Sprintf("f%d", want), 0)
		if !ok || got != want {
			t.Fatalf("Alloc: got (%d,%v), want (%d,true)", got, ok, want)
		}
	}
	if p.CanAlloc("fx", 0) {
		t.Fatal("CanAlloc should be false when exhausted")
	}
	if _, ok := p.Alloc("fx", 0); ok {
		t.Fatal("Alloc should fail when exhausted")
	}
}

// 隔离：恰等即可用，小 1 不可用；隔离中的标签不得给别的 fec。
func TestQuarantineBoundary(t *testing.T) {
	p := mustPool(t, 100, 101, 50)
	p.Alloc("f", 0) // 100
	p.Alloc("g", 0) // 101
	p.Free("g", 101, 10)
	if p.CanAlloc("h", 59) {
		t.Fatal("t=59: quarantined label must not be allocatable")
	}
	if _, ok := p.Alloc("h", 59); ok {
		t.Fatal("t=59: Alloc must fail (101 quarantined until 60)")
	}
	if !p.CanAlloc("h", 60) {
		t.Fatal("t=60: quarantine expired, must be allocatable")
	}
	got, ok := p.Alloc("h", 60)
	if !ok || got != 101 {
		t.Fatalf("t=60: got (%d,%v), want (101,true)", got, ok)
	}
}

// 亲和：释放者在隔离期内取回原标签；到期后亲和失效。
func TestAffinity(t *testing.T) {
	p := mustPool(t, 100, 102, 50)
	p.Alloc("f", 0) // 100
	p.Alloc("g", 0) // 101
	p.Free("f", 100, 10)
	p.Free("g", 101, 10)
	got, ok := p.Alloc("f", 20)
	if !ok || got != 100 {
		t.Fatalf("affinity: got (%d,%v), want (100,true)", got, ok)
	}
	p.Free("f", 100, 30)
	// 到期后亲和失效：别的 fec 可取走 100。
	got, ok = p.Alloc("h", 80)
	if !ok || got != 100 {
		t.Fatalf("after expiry: got (%d,%v), want (100,true)", got, ok)
	}
	got, ok = p.Alloc("f", 81)
	if !ok || got != 101 {
		t.Fatalf("affinity lost: got (%d,%v), want (101,true)", got, ok)
	}
}

// probes：取最小可用标签考察的标签数 ≤ 本次落地到期数+32，与已分配数无关。
func TestProbesIndependentOfAllocated(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			p := mustPool(t, MinLabel, MinLabel+n+9, 10)
			for i := 0; i < n; i++ {
				if _, ok := p.Alloc(fmt.Sprintf("f%d", i), 0); !ok {
					t.Fatalf("alloc %d failed", i)
				}
			}
			const freed = 5
			for i := 0; i < freed; i++ {
				p.Free(fmt.Sprintf("f%d", i), MinLabel+i, 0)
			}
			if _, ok := p.Alloc("new", 20); !ok {
				t.Fatal("alloc after landing failed")
			}
			if p.probes > freed+32 {
				t.Fatalf("probes=%d, want <= %d (landed %d + 32)", p.probes, freed+32, freed)
			}
			// 无到期可落地时：考察数不超过 32。
			if _, ok := p.Alloc("new2", 21); !ok {
				t.Fatal("second alloc failed")
			}
			if p.probes > 32 {
				t.Fatalf("probes=%d with nothing to land, want <= 32", p.probes)
			}
		})
	}
}

// AllocRaw：日志重放直接占用空闲或隔离中的标签。
func TestAllocRaw(t *testing.T) {
	p := mustPool(t, 100, 102, 50)
	p.Alloc("f", 0) // 100
	p.Free("f", 100, 10)
	if err := p.AllocRaw("g", 100); err != nil { // 隔离中的标签可被重放占用
		t.Fatalf("AllocRaw quarantined: %v", err)
	}
	if err := p.AllocRaw("h", 100); err != ErrCorrupt {
		t.Fatalf("AllocRaw allocated: got %v, want ErrCorrupt", err)
	}
	if err := p.AllocRaw("h", 999); err != ErrCorrupt {
		t.Fatalf("AllocRaw out of range: got %v, want ErrCorrupt", err)
	}
	if err := p.AllocRaw("h", 101); err != nil {
		t.Fatalf("AllocRaw free: %v", err)
	}
}
