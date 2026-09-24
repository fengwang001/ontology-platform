package etime

import "testing"

// TestIngestCheckCountConstant proves maxEt is a single incrementally
// maintained integer: after m on-time ingests, one more on-time ingest
// examines only a constant number of events, never O(m).
func TestIngestCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, err := New(5)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if !c.Ingest(int64(i)) {
				t.Fatalf("m=%d: event %d should be on time", m, i)
			}
		}
		if !c.Ingest(int64(m)) {
			t.Fatalf("m=%d: final event should be on time", m)
		}
		if c.checked > 2 {
			t.Fatalf("m=%d: checked %d events, want <= 2 (constant)", m, c.checked)
		}
	}
}

// TestClockSemantics pins ETW/maxEt advancement and lateness judgment.
func TestClockSemantics(t *testing.T) {
	c, err := New(5)
	if err != nil {
		t.Fatal(err)
	}
	if c.ETW() != negInf || c.MaxEt() != negInf {
		t.Fatal("empty clock must report negative infinity")
	}
	steps := []struct {
		et     int64
		onTime bool
		etw    int64
	}{
		{10, true, 5},
		{8, true, 5},   // 8 > 5, maxEt unchanged
		{20, true, 15}, // advances maxEt
		{14, false, 15},
		{15, false, 15}, // et == ETW is late
		{16, true, 15},
	}
	for i, s := range steps {
		if got := c.Ingest(s.et); got != s.onTime {
			t.Fatalf("step %d: Ingest(%d) onTime=%v, want %v", i, s.et, got, s.onTime)
		}
		if c.ETW() != s.etw {
			t.Fatalf("step %d: ETW=%d, want %d", i, c.ETW(), s.etw)
		}
	}
	if c.MaxEt() != 20 {
		t.Fatalf("MaxEt=%d, want 20", c.MaxEt())
	}
}

// TestNegativeLatenessRejected pins the sentinel error and no state.
func TestNegativeLatenessRejected(t *testing.T) {
	if _, err := New(-1); err != ErrNegativeLateness {
		t.Fatalf("err=%v, want ErrNegativeLateness", err)
	}
}
