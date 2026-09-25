package wjoin

import (
	"fmt"
	"testing"
)

// The cleanup after a watermark advance must examine only the buckets that
// actually close: buckets are located in window-end order, not by full scan.
func TestCleanupCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		j, err := New(10, 1<<40) // huge delay: nothing closes early
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, 0, m+1)
		for i := 0; i < m; i++ {
			evs = append(evs, Event{Key: fmt.Sprint(i), TS: 0, Side: 'L'}) // window [0,10)
		}
		evs = append(evs, Event{Key: "victim", TS: -10, Side: 'L'}) // window [-10,0), end 0
		if _, err := j.Feed(evs); err != nil {
			t.Fatal(err)
		}
		// wm advances to 0: closes exactly the victim bucket
		if _, err := j.Feed([]Event{{Key: "t", TS: 1 << 40, Side: 'R'}}); err != nil {
			t.Fatal(err)
		}
		if j.checked != 1 {
			t.Fatalf("m=%d: checked=%d, want 1 (only the closed bucket)", m, j.checked)
		}
		if j.retained != m+1 { // m open buckets + the trigger event
			t.Fatalf("m=%d: retained=%d, want %d", m, j.retained, m+1)
		}
	}
}

// Watermark advance must clear every closed bucket, in end order.
func TestCleanupClearsInOrder(t *testing.T) {
	j, _ := New(10, 0)
	j.Feed([]Event{
		{Key: "a", TS: 1, Side: 'L'},  // [0,10)
		{Key: "a", TS: 11, Side: 'L'}, // [10,20)
		{Key: "a", TS: 21, Side: 'L'}, // [20,30), wm=21 -> clears [0,10),[10,20)
	})
	if j.retained != 1 {
		t.Fatalf("retained=%d, want 1", j.retained)
	}
	j.Flush()
	if j.retained != 0 || len(j.buckets) != 0 || len(j.ends) != 0 {
		t.Fatalf("flush: retained=%d buckets=%d ends=%d", j.retained, len(j.buckets), len(j.ends))
	}
}
