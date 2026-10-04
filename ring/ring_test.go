package ring

import "testing"

func TestAddEvictsOldestAndCountsIncrementally(t *testing.T) {
	r := New(3)
	r.Add(true, false)  // fail
	r.Add(false, true)  // slow
	r.Add(false, false) // ok
	if r.Count() != 3 || r.Failures() != 1 || r.Slows() != 1 {
		t.Fatalf("got count=%d fails=%d slows=%d", r.Count(), r.Failures(), r.Slows())
	}
	r.Add(true, true) // evicts the oldest (fail) entry
	if r.Count() != 3 || r.Failures() != 1 || r.Slows() != 2 {
		t.Fatalf("after evict: got count=%d fails=%d slows=%d",
			r.Count(), r.Failures(), r.Slows())
	}
	r.Clear()
	if r.Count() != 0 || r.Failures() != 0 || r.Slows() != 0 {
		t.Fatalf("after clear: got count=%d fails=%d slows=%d",
			r.Count(), r.Failures(), r.Slows())
	}
	r.Add(true, true) // still usable after clear
	if r.Count() != 1 || r.Failures() != 1 || r.Slows() != 1 {
		t.Fatalf("reuse after clear: got count=%d fails=%d slows=%d",
			r.Count(), r.Failures(), r.Slows())
	}
}

// TestAddExaminesConstantSlots proves via the unexported examined
// counter that each Add touches O(1) slots, independent of capacity:
// once full, every Add examines exactly 2 slots (evict + insert) for
// both N=10 and N=1000, and Clear examines none.
func TestAddExaminesConstantSlots(t *testing.T) {
	const extra = 5000
	for _, n := range []int{10, 1000} {
		r := New(n)
		for i := 0; i < n; i++ {
			r.Add(i%2 == 0, i%3 == 0)
		}
		base := r.examined
		for i := 0; i < extra; i++ {
			r.Add(i%2 == 0, i%3 == 0)
		}
		if got := r.examined - base; got != 2*extra {
			t.Errorf("N=%d: %d adds examined %d slots, want %d", n, extra, got, 2*extra)
		}
		fails, slows := 0, 0
		for _, e := range r.slots {
			if e.fail {
				fails++
			}
			if e.slow {
				slows++
			}
		}
		if fails != r.Failures() || slows != r.Slows() {
			t.Errorf("N=%d: incremental counters fails=%d slows=%d, full scan fails=%d slows=%d",
				n, r.Failures(), r.Slows(), fails, slows)
		}
	}
	r := New(1000)
	for i := 0; i < 1000; i++ {
		r.Add(true, true)
	}
	before := r.examined
	r.Clear()
	if r.examined != before {
		t.Errorf("Clear examined %d slots", r.examined-before)
	}
}
