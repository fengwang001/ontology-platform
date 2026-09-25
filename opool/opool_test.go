package opool

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/blk"
)

func TestBlockAndStack(t *testing.T) {
	b := blk.New(7)
	if b.ID() != 7 || b.State() != blk.InUse {
		t.Fatal("a new block must be in-use")
	}
	tr := func(f func() error, want blk.State, bad error) {
		if err := f(); (err == nil) != (bad == nil) || b.State() != want {
			t.Fatalf("state=%v err=%v want=%v", b.State(), err, want)
		}
	}
	tr(b.ToIdle, blk.Idle, nil)
	tr(b.ToIdle, blk.Idle, blk.ErrNotInUse)
	tr(b.ToInUse, blk.InUse, nil)
	tr(b.ToInUse, blk.InUse, blk.ErrNotIdle)
	tr(b.Reclaim, blk.Reclaimed, nil)
	tr(b.ToIdle, blk.Reclaimed, blk.ErrNotInUse)
	var s blk.Stack
	for i := range 5 {
		s.Push(blk.New(uint64(i)))
	}
	for w := 4; w >= 0; w-- {
		g, ok := s.Pop()
		if !ok || g.ID() != uint64(w) {
			t.Fatalf("LIFO pop want b%d", w)
		}
	}
	if blk.AtCapacity(1, 2) || !blk.AtCapacity(2, 2) {
		t.Fatal("AtCapacity boundary")
	}
}

func TestEightSteps(t *testing.T) {
	p, _ := New(2)
	g := [3]*blk.Block{}
	for i := range g {
		g[i], _ = p.Acquire()
	}
	want := [3][2]int{{1, 3}, {2, 3}, {2, 2}}
	for k, b := range g {
		if err := p.Release(b); err != nil || p.Idle() != want[k][0] || p.Total() != want[k][1] {
			t.Fatalf("release %d: idle=%d total=%d", k, p.Idle(), p.Total())
		}
	}
	a7, _ := p.Acquire()
	if a7 != g[1] || p.Idle() != 1 || p.Total() != 2 {
		t.Fatal("step 7 must pop LIFO top b1")
	}
	a8, _ := p.Acquire()
	if a8 != g[0] || p.Idle() != 0 || p.Total() != 2 || g[2].State() != blk.Reclaimed {
		t.Fatal("step 8 must pop b0; b2 must be reclaimed")
	}
}

// 不变量1守恒唯一 + 2与朴素LIFO一致 + 3回收块不再返回、Idle<=cap（多档循环）。
func TestConservationAndNaive(t *testing.T) {
	for _, max := range []int{1, 2, 8} {
		p, _ := New(max)
		rng := rand.New(rand.NewSource(int64(max)*7 + 1))
		dead := map[*blk.Block]bool{}
		var idle, held []*blk.Block // 朴素空闲栈 / 朴素 in-use 集合
		for n := 0; n < 600; n++ {
			if rng.Intn(2) == 0 || len(held) == 0 {
				b, _ := p.Acquire()
				if dead[b] {
					t.Fatal("evicted block handed out again")
				}
				if len(idle) > 0 {
					want := idle[len(idle)-1]
					idle = idle[:len(idle)-1]
					if b != want {
						t.Fatalf("step %d LIFO got b%d want b%d", n, b.ID(), want.ID())
					}
				}
				held = append(held, b)
			} else {
				i := rng.Intn(len(held))
				b := held[i]
				held = append(held[:i], held[i+1:]...)
				if err := p.Release(b); err != nil {
					t.Fatal(err)
				}
				if len(idle) >= max {
					dead[b] = true
				} else {
					idle = append(idle, b)
				}
			}
			if p.Total() != len(held)+len(idle) || p.Idle() != len(idle) || p.Idle() > max {
				t.Fatalf("step %d total=%d idle=%d naive(t=%d i=%d) max=%d",
					n, p.Total(), p.Idle(), len(held)+len(idle), len(idle), max)
			}
		}
	}
}

func TestRejectedNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidMaxIdle) {
		t.Fatalf("New(0): %v", err)
	}
	if ErrInvalidMaxIdle == ErrDuplicateRelease || ErrDuplicateRelease == ErrUnknownBlock ||
		ErrInvalidMaxIdle == ErrUnknownBlock {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	p, _ := New(1)
	b, _ := p.Acquire()
	_ = p.Release(b)
	i0, t0 := p.Idle(), p.Total()
	cases := []struct {
		name string
		f    func() error
		bad  error
	}{
		{"duplicate", func() error { return p.Release(b) }, ErrDuplicateRelease},
		{"unknown", func() error { return p.Release(blk.New(999)) }, ErrUnknownBlock},
	}
	for _, c := range cases {
		if err := c.f(); !errors.Is(err, c.bad) || p.Idle() != i0 || p.Total() != t0 {
			t.Fatalf("%s rejection left a trace", c.name)
		}
	}
	if c, err := p.Acquire(); err != nil || c != b {
		t.Fatal("pool unusable after a rejection")
	}
}

func TestCheckedRecordsO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		p, _ := New(m)
		bs := make([]*blk.Block, m)
		for i := range bs {
			bs[i], _ = p.Acquire()
		}
		for _, b := range bs {
			_ = p.Release(b)
		}
		if _, err := p.Acquire(); err != nil || p.checked > 1 {
			t.Fatalf("m=%d checked=%d err=%v", m, p.checked, err)
		}
	}
}
