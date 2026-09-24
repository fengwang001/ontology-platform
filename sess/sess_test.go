package sess

import (
	"errors"
	"math/rand"
	"sort"
	"testing"

	"ontology/evt"
)

// recompute：全部事件排序后从头扫一遍分会话（题面参考实现）。
func recompute(ts []int64, gap int64) []Session {
	cp := append([]int64(nil), ts...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	var out []Session
	for _, t := range cp {
		if n := len(out); n > 0 && t-out[n-1].End <= gap {
			out[n-1].End, out[n-1].N = t, out[n-1].N+1
		} else {
			out = append(out, Session{t, t, 1})
		}
	}
	return out
}

// canonicalOK 校验不变量 3：N>0、不重叠、start 严格升序、相邻间隔严格 >gap。
func canonicalOK(ss []Session, gap int64) bool {
	for i, x := range ss {
		if x.N <= 0 || x.Start > x.End {
			return false
		}
		if i > 0 && (x.Start <= ss[i-1].Start || x.Start-ss[i-1].End <= gap) {
			return false
		}
	}
	return true
}

func TestSixStepSequence(t *testing.T) {
	want := [][]Session{
		{{100, 100, 1}}, {{100, 105, 2}},
		{{100, 105, 2}, {130, 130, 1}},
		{{100, 105, 2}, {130, 135, 2}},
		{{100, 105, 2}, {118, 118, 1}, {130, 135, 2}},
		{{100, 105, 3}, {118, 118, 1}, {130, 135, 2}},
	}
	s, _ := NewSet(10, 0)
	for i, ts := range []int64{100, 105, 130, 135, 118, 100} {
		s.Add(evt.Event{Key: "k", TS: ts})
		if got := s.Sessions("k"); !Equal(got, want[i]) {
			t.Fatalf("step %d: %v != %v", i+1, got, want[i])
		}
	}
}

func TestCanonicalForm(t *testing.T) {
	for _, c := range []struct {
		gap int64
		ts  []int64
	}{{10, []int64{100, 105, 130, 135, 118, 100}}, {3, []int64{0, 4, 8, 1}}, {10, []int64{0}}} {
		s, _ := NewSet(c.gap, 0)
		for _, x := range c.ts {
			s.Add(evt.Event{Key: "k", TS: x})
		}
		if got := s.Sessions("k"); !canonicalOK(got, c.gap) {
			t.Errorf("gap=%d: %v not canonical", c.gap, got)
		}
	}
}

func TestOrderIndependence(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 3, 10, 100, 500} {
		for trial := 0; trial < 10; trial++ {
			gap, ts := int64(1+r.Intn(10)), make([]int64, n)
			for i := range ts {
				ts[i] = int64(r.Intn(3*n + 1))
			}
			ref := recompute(ts, gap)
			for round := 0; round < 5; round++ {
				s, _ := NewSet(gap, 0)
				for _, q := range r.Perm(n) {
					s.Add(evt.Event{Key: "k", TS: ts[q]})
				}
				if got := s.Sessions("k"); !Equal(got, ref) || !canonicalOK(got, gap) {
					t.Fatalf("n=%d trial=%d round=%d: %v != %v", n, trial, round, got, ref)
				}
			}
		}
	}
}

func TestRejectionsNoTrace(t *testing.T) {
	for _, g := range []int64{0, -1} {
		if _, e := NewSet(g, 0); !errors.Is(e, ErrBadGap) {
			t.Fatalf("gap=%d: %v", g, e)
		}
	}
	if ErrBadGap == ErrTooMany || ErrTooMany == evt.ErrInvalidEvent {
		t.Fatal("sentinel errors must be distinct")
	}
	for _, c := range []struct {
		max  int
		seed []int64
		e    evt.Event
		want error
	}{
		{0, []int64{1, 2}, evt.Event{Key: "", TS: 5}, evt.ErrInvalidEvent},
		{2, []int64{0, 100}, evt.Event{Key: "k", TS: 200}, ErrTooMany},
	} {
		s, _ := NewSet(10, c.max)
		for _, x := range c.seed {
			s.Add(evt.Event{Key: "k", TS: x})
		}
		before := s.Sessions("k")
		if err := s.Add(c.e); !errors.Is(err, c.want) {
			t.Errorf("got %v want %v", err, c.want)
		}
		if !Equal(s.Sessions("k"), before) {
			t.Error("state changed after rejection")
		}
		if err := s.Add(evt.Event{Key: "k", TS: 0}); err != nil {
			t.Errorf("set unusable after rejection: %v", err)
		}
	}
}

func TestComparisonBudget(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		for _, op := range []struct{ base, delta, cmp, d int64 }{
			{0, 0, 1, 0}, {0, 5, 1, 0}, {0, 11, 1, 1}, {1, 10, 2, -1},
		} {
			s, _ := NewSet(10, 0)
			for i := int64(0); i < m; i++ {
				s.Add(evt.Event{Key: "k", TS: i * 11})
			}
			ts := (m-1-op.base)*11 + op.delta
			if err := s.Add(evt.Event{Key: "k", TS: ts}); err != nil {
				t.Fatal(err)
			}
			if int64(s.cmp) != op.cmp {
				t.Errorf("m=%d: cmp=%d want %d (must not grow with m)", m, s.cmp, op.cmp)
			}
			if n := int64(len(s.Sessions("k"))); n != m+op.d {
				t.Errorf("m=%d: sessions=%d want %d", m, n, m+op.d)
			}
		}
	}
}
