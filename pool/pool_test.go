package pool

import (
	"errors"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

type naive struct {
	f  []FrameSnapshot
	ix map[int]int
	w  int
}

func (m *naive) do(k byte, x int) (int, error) {
	if k == 'P' {
		if f, ok := m.ix[x]; ok {
			m.f[f].Pin++
			return f, nil
		}
		t := slices.IndexFunc(m.f, func(z FrameSnapshot) bool { return z.PageID == -1 })
		if t < 0 {
			t = slices.IndexFunc(m.f, func(z FrameSnapshot) bool { return z.Pin == 0 })
		}
		if t < 0 {
			return -1, ErrNoEvictableFrame
		}
		if v := &m.f[t]; v.PageID != -1 {
			if v.Dirty {
				m.w++
			}
			delete(m.ix, v.PageID)
		}
		m.f[t] = FrameSnapshot{PageID: x, Pin: 1}
		m.ix[x] = t
		return t, nil
	}
	if x < 0 || x >= len(m.f) {
		return 0, ErrInvalidFrame
	}
	if k == 'U' {
		if m.f[x].Pin == 0 {
			return 0, ErrUnpinNotPinned
		}
		m.f[x].Pin--
	} else {
		m.f[x].Dirty = true
	}
	return 0, nil
}
func pdo(p *Pool, k byte, x int) (int, error) {
	if k == 'P' {
		return p.Pin(x)
	}
	if k == 'U' {
		return 0, p.Unpin(x)
	}
	return 0, p.MarkDirty(x)
}
func mustPin(t *testing.T, p *Pool, id int) {
	t.Helper()
	if _, e := p.Pin(id); e != nil {
		t.Fatal(e)
	}
}
func TestNaiveReference(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99} {
		r := rand.New(rand.NewSource(seed))
		for _, n := range []int{1, 2, 3, 5} {
			p := New(n)
			m := &naive{f: slices.Repeat([]FrameSnapshot{{PageID: -1}}, n), ix: map[int]int{}}
			for k := 0; k < 2000; k++ {
				kind := []byte{'P', 'P', 'U', 'D'}[r.Intn(4)]
				x := r.Intn(n + 4)
				rf, re := pdo(p, kind, x)
				mf, me := m.do(kind, x)
				if (re == nil) != (me == nil) || !reflect.DeepEqual(p.Snapshot(), m.f) || p.Writes() != m.w || (re == nil && kind == 'P' && rf != mf) {
					t.Fatalf("seed=%d n=%d k=%d %c(%d) real=%v naive=%v", seed, n, k, kind, x, re, me)
				}
			}
		}
	}
}
func TestUniquenessConservation(t *testing.T) {
	p := New(4)
	for k := 0; k < 500; k++ {
		if _, e := p.Pin((k*7 + 3) % 9); e == nil && k%3 == 0 {
			_ = p.Unpin(k % 4)
		}
	}
	seen := map[int]int{}
	for _, f := range p.Snapshot() {
		if seen[f.PageID]++; f.PageID != -1 && seen[f.PageID] > 1 {
			t.Fatalf("page %d resident twice", f.PageID)
		}
	}
}
func TestPinnedNeverEvicted(t *testing.T) {
	p := New(3)
	for _, id := range []int{10, 20, 30} {
		mustPin(t, p, id)
	}
	if _, err := p.Pin(40); !errors.Is(err, ErrNoEvictableFrame) {
		t.Fatalf("want ErrNoEvictableFrame, got %v", err)
	}
	if !reflect.DeepEqual(p.Snapshot(), []FrameSnapshot{{10, false, 1}, {20, false, 1}, {30, false, 1}}) {
		t.Fatalf("pinned page disturbed: %v", p.Snapshot())
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	try := func(name string, pre func(*Pool), run func(*Pool) error) {
		p := New(1)
		mustPin(t, p, 1)
		if pre != nil {
			pre(p)
		}
		before, bw := p.Snapshot(), p.Writes()
		if err := run(p); err == nil {
			t.Fatalf("%s: expected error", name)
		}
		if !reflect.DeepEqual(before, p.Snapshot()) || bw != p.Writes() {
			t.Fatalf("%s: rejected op mutated state", name)
		}
		mustPin(t, p, 1)
	}
	u0 := func(p *Pool) { _ = p.Unpin(0) }
	try("double-unpin", u0, func(p *Pool) error { return p.Unpin(0) })
	try("bad-frame-unpin", nil, func(p *Pool) error { return p.Unpin(9) })
	try("bad-frame-dirty", nil, func(p *Pool) error { return p.MarkDirty(-1) })
	try("full-all-pinned", nil, func(p *Pool) error { _, e := p.Pin(2); return e })
}
func TestErrorsDistinct(t *testing.T) {
	if ErrUnpinNotPinned == ErrInvalidFrame || ErrInvalidFrame == ErrNoEvictableFrame || ErrUnpinNotPinned == ErrNoEvictableFrame {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}
func TestResidentLookupConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		p := New(m)
		for i := 0; i < m; i++ {
			mustPin(t, p, i)
		}
		p.lastPinChecks = 0
		if _, e := p.Pin(m / 2); e != nil || p.lastPinChecks != 1 {
			t.Fatalf("m=%d: %d frames, want 1 (%v)", m, p.lastPinChecks, e)
		}
	}
}
