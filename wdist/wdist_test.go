package wdist

import "testing"

func feedT(t *testing.T, c *Counter, evs []Event) {
	t.Helper()
	if err := c.Check(evs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.Apply(evs)
}

// TestExpireCheckBounded: m wholly-expired buckets accumulate; the
// trigger that advances T compares only a constant number of bucket
// boundaries regardless of m. checked is unexported and read only by
// this same-package test.
func TestExpireCheckBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c := New(10, 5)
		evs := make([]Event, m)
		for i := 0; i < m; i++ { // one key per bucket 0..m-1, far in the past
			evs[i] = Event{Key: i, TS: int64(i) * 5}
		}
		feedT(t, c, evs)
		jump := Event{Key: 999999, TS: int64(m)*5 + 100} // T jumps past every bucket
		feedT(t, c, []Event{jump})
		if c.checked > 1 {
			t.Fatalf("m=%d: boundary comparisons grew with m: %d", m, c.checked)
		}
		if c.Distinct() != 1 {
			t.Fatalf("m=%d: only the trigger key should survive, got %d", m, c.Distinct())
		}
	}
}

// TestLastMonotonic: last[key] never decreases across accepted events.
func TestLastMonotonic(t *testing.T) {
	cases := []struct {
		name string
		evs  []Event
		want map[int]int64
	}{
		{"repeat moves forward", []Event{{1, 1}, {1, 1}, {1, 6}, {1, 6}}, map[int]int64{1: 6}},
		{"two keys", []Event{{2, 3}, {5, 4}, {2, 7}}, map[int]int64{2: 7, 5: 4}},
		{"expire then reappear", []Event{{1, 0}, {2, 20}, {1, 21}}, map[int]int64{1: 21, 2: 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(10, 5)
			var prev map[int]int64
			for _, e := range tc.evs {
				feedT(t, c, []Event{e})
				for k, v := range c.last { // invariant after every event
					if prev != nil && prev[k] > v {
						t.Fatalf("last[%d] decreased %d -> %d", k, prev[k], v)
					}
				}
				prev = map[int]int64{}
				for k, v := range c.last {
					prev[k] = v
				}
			}
			for k, v := range tc.want {
				if c.last[k] != v {
					t.Fatalf("last[%d]=%d want %d", k, c.last[k], v)
				}
			}
		})
	}
}

// TestSixSteps mirrors NOTES.md: per-step T and Distinct, W=10 b=5.
func TestSixSteps(t *testing.T) {
	evs := []Event{{1, 1}, {2, 3}, {5, 4}, {1, 6}, {3, 11}, {4, 13}}
	wantLive := []int{1, 2, 3, 3, 4, 4}
	wantT := []int64{1, 3, 4, 6, 11, 13}
	c := New(10, 5)
	for i, e := range evs {
		feedT(t, c, []Event{e})
		if c.T() != wantT[i] || c.Distinct() != wantLive[i] {
			t.Fatalf("step %d: T=%d live=%d want T=%d live=%d", i+1, c.T(), c.Distinct(), wantT[i], wantLive[i])
		}
	}
}
