package seglog

import (
	"fmt"
	"testing"
)

// Invariant 3: live segment count stays within the constant bound
// T-1 no matter how many records are appended (m up to 10000).
func TestLiveSegmentsBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		l := New(8, 3)
		maxLive := 0
		for i := 0; i < m; i++ {
			l.Append(fmt.Sprintf("key-%d", i), "v")
			if l.liveSegs > maxLive {
				maxLive = l.liveSegs
			}
		}
		if maxLive > l.T-1 {
			t.Fatalf("m=%d: live segments peaked at %d, want <= %d", m, maxLive, l.T-1)
		}
		if l.liveSegs >= l.T {
			t.Fatalf("m=%d: final live segments %d, want < %d", m, l.liveSegs, l.T)
		}
	}
	// The bound is a small constant, not linear in m: peak for
	// m=100 equals peak for m=10000.
	peak := func(m int) int {
		l := New(8, 3)
		p := 0
		for i := 0; i < m; i++ {
			l.Append(fmt.Sprintf("key-%d", i), "v")
			if l.liveSegs > p {
				p = l.liveSegs
			}
		}
		return p
	}
	if peak(100) != peak(10000) {
		t.Fatalf("peak grows with m: %d vs %d", peak(100), peak(10000))
	}
}

// Get scans buffer newest-first, then segments newest-first.
func TestGetOrder(t *testing.T) {
	l := New(4, 2)
	l.Append("a", "old")
	l.Append("b", "x") // buffer hits 4 -> flush
	l.Append("a", "new")
	if v, ok := l.Get("a"); !ok || v != "new" {
		t.Fatalf("Get(a)=%q,%v want new,true", v, ok)
	}
	l.Append("c", "y") // buffer hits 4 -> flush, 2 segs -> merge
	if v, ok := l.Get("a"); !ok || v != "new" {
		t.Fatalf("after merge Get(a)=%q,%v want new,true", v, ok)
	}
	if _, ok := l.Get("zz"); ok {
		t.Fatal("Get on absent key returned ok")
	}
}
