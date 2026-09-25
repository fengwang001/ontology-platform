package sched

import (
	"math/bits"
	"strconv"
	"testing"
)

// TestHeapSelectionSublinear pins dispatch selection to O(log m): with m jobs
// already ready, the private counter of one dispatch must stay within a small
// constant times log2(m) at several sizes. A linear scan would be ~m, which
// exceeds the bound by orders of magnitude at m=10000. The counter is read
// only here inside the package; no exported API exposes its value.
func TestHeapSelectionSublinear(t *testing.T) {
	const c = 4
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.Add("j"+strconv.Itoa(i), 0, int64(m-i)); err != nil {
				t.Fatalf("m=%d Add: %v", m, err)
			}
		}
		s.prepare()
		if !s.next() {
			t.Fatalf("m=%d: dispatch produced nothing", m)
		}
		if b := c * bits.Len(uint(m)); s.inspect > b {
			t.Errorf("m=%d: examined %d nodes, bound c*log2(m)=%d", m, s.inspect, b)
		}
		if s.inspect >= m/4 {
			t.Errorf("m=%d: %d comparisons look linear, not heap selection", m, s.inspect)
		}
	}
}

// TestHeapCounterBoundedPerDispatch runs jobs with staggered arrivals and
// checks every dispatch's comparison count against the ready-set log bound.
func TestHeapCounterBoundedPerDispatch(t *testing.T) {
	for _, m := range []int{100, 1000} {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.Add("k"+strconv.Itoa(i), int64(i), 1); err != nil {
				t.Fatal(err)
			}
		}
		s.prepare()
		for d := 0; d < m; d++ {
			if !s.next() {
				t.Fatalf("m=%d dispatch %d: nothing", m, d)
			}
			if s.inspect > 4*bits.Len(uint(m)) {
				t.Errorf("m=%d dispatch=%d: %d comparisons exceed 4*log2(m)", m, d, s.inspect)
			}
		}
	}
}
