package backpressure

import "testing"

// Regression: Add with n <= 0 used to add -n to Rejected; a
// non-positive amount must be invalid without touching any counter.
func TestRegressionNonPositiveAddLeavesRejected(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	for _, n := range []int64{0, -1, -100} {
		if c.Add(n) {
			t.Fatalf("Add(%d) accepted, want rejected", n)
		}
	}
	if got := c.Stat(); got.Rejected != 0 || got.Level != 0 {
		t.Fatalf("got %+v, want Level 0 Rejected 0", got)
	}
}

// Regression: rejecting an Add over the hard limit used to bump
// Rejected by 1; it must accumulate the rejected amount n.
func TestRegressionRejectedAccumulatesAmount(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	c.Add(5)
	for i := 0; i < 3; i++ {
		if c.Add(10) {
			t.Fatal("Add(10) over hard limit accepted")
		}
	}
	if got := c.Stat(); got.Rejected != 30 {
		t.Fatalf("got Rejected %d, want 30 (3 x 10)", got.Rejected)
	}
}

// Regression: Sub resumed only when level < low; reaching exactly
// the low watermark must already resume Flowing.
func TestRegressionResumeAtExactlyLow(t *testing.T) {
	c := mustNew(t, 10, 20, 100)
	c.Add(20) // Paused
	c.Sub(10) // 10 == low -> Flowing
	if got := c.Stat(); got.State != Flowing || got.Resumes != 1 {
		t.Fatalf("got %+v, want Flowing with 1 resume", got)
	}
}
