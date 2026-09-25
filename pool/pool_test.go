package pool

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

func newPool(t *testing.T, n int) *Pool {
	p, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	return p
}
func TestFaultInjection(t *testing.T) {
	cases := []struct {
		name string
		run  func(p *Pool) error
		want error
	}{
		{"double-unpin", func(p *Pool) error {
			_, _ = p.Pin(1)
			if err := p.Unpin(0); err != nil {
				return err
			}
			return p.Unpin(0)
		}, ErrNotPinned},
		{"unpin-bad-frame", func(p *Pool) error { return p.Unpin(7) }, ErrBadFrame},
		{"unpin-negative-frame", func(p *Pool) error { return p.Unpin(-1) }, ErrBadFrame},
		{"markdirty-bad-frame", func(p *Pool) error { return p.MarkDirty(3) }, ErrBadFrame},
		{"pool-full-all-pinned", func(p *Pool) error {
			_, _ = p.Pin(1)
			_, _ = p.Pin(2)
			_, err := p.Pin(3)
			return err
		}, ErrPoolFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(newPool(t, 2)); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
	if errors.Is(ErrNotPinned, ErrBadFrame) || errors.Is(ErrNotPinned, ErrPoolFull) ||
		errors.Is(ErrBadFrame, ErrPoolFull) {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestFailureNoTrace(t *testing.T) {
	p := newPool(t, 2)
	_, _ = p.Pin(1)
	_, _ = p.Pin(2)
	_ = p.MarkDirty(0)
	rejected := []func() error{
		func() error { return p.Unpin(9) },
		func() error { return p.MarkDirty(-1) },
		func() error { _, err := p.Pin(3); return err }, // all pinned
	}
	for i, op := range rejected {
		snap, w := p.Snapshot(), p.Writes()
		if err := op(); err == nil {
			t.Fatalf("case %d: expected rejection", i)
		}
		if p.Writes() != w || !slices.Equal(snap, p.Snapshot()) {
			t.Fatalf("case %d: rejected op changed state", i)
		}
	}
	if _, err := p.Pin(1); err != nil { // still usable afterwards
		t.Fatalf("pool unusable after rejections: %v", err)
	}
}
func TestComplexityCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		p := newPool(t, m)
		for i := 0; i < 2; i++ { // second Pin is the resident hit
			if _, err := p.Pin(42); err != nil {
				t.Fatalf("m=%d: %v", m, err)
			}
		}
		if p.checked > 1 {
			t.Fatalf("m=%d: inspected %d frames, want <= 1", m, p.checked)
		}
	}
}
func TestPinSafety(t *testing.T) {
	p := newPool(t, 2)
	_, _ = p.Pin(1)
	_, _ = p.Pin(2) // f0, f1
	_ = p.MarkDirty(0)
	if _, err := p.Pin(3); !errors.Is(err, ErrPoolFull) {
		t.Fatalf("pinned frames must block eviction, got %v", err)
	}
	_ = p.Unpin(1)
	f, err := p.Pin(3) // evicts f1 (clean); page1 must stay put
	if err != nil || f != 1 {
		t.Fatalf("Pin(3) = f%d, %v; want f1", f, err)
	}
	if s := p.Snapshot(); s[0].PageID != 1 || !s[0].Dirty || s[0].Pin != 1 {
		t.Fatalf("pinned page1 disturbed: %+v", s[0])
	}
	if w := p.Writes(); w != 0 {
		t.Fatalf("clean eviction must not write back, writes=%d", w)
	}
}
func TestConcurrentPin(t *testing.T) {
	const n = 64
	p := newPool(t, n)
	done := make(chan struct{})
	var rwg sync.WaitGroup
	rwg.Add(1)
	go func() { // resident count must never decrease while only reading
		defer rwg.Done()
		for prev := 0; ; {
			select {
			case <-done:
				return
			default:
			}
			c := p.ResidentCount()
			if c < prev {
				t.Errorf("resident count decreased: %d -> %d", prev, c)
				return
			}
			prev = c
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); _, _ = p.Pin(id) }(i)
	}
	wg.Wait()
	close(done)
	rwg.Wait()
	if p.ResidentCount() != n || p.Writes() != 0 {
		t.Fatalf("resident=%d writes=%d, want %d,0", p.ResidentCount(), p.Writes(), n)
	}
	seen := map[int]bool{}
	for _, f := range p.Snapshot() {
		if f.Empty() || f.Pin != 1 || seen[f.PageID] {
			t.Fatalf("bad frame after concurrent pins: %+v", f)
		}
		seen[f.PageID] = true
	}
}
