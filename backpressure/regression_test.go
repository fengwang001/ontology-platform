package backpressure

import "testing"

// Regression: Add with n <= 0 used to do `rejected += -n`, polluting
// the Rejected counter; a non-positive amount must change nothing.
func TestAddNonPositiveLeavesRejectedUntouched(t *testing.T) {
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

// Regression: the hard-limit branch used `rejected++`, counting
// rejections instead of summing the rejected amounts.
func TestRejectedAccumulatesAmounts(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	c.Add(10) // fill to hard
	for i := 0; i < 3; i++ {
		if c.Add(10) {
			t.Fatal("Add(10) beyond hard accepted")
		}
	}
	if got := c.Stat(); got.Rejected != 30 {
		t.Fatalf("got Rejected %d, want 30 (3 rejections of 10)", got.Rejected)
	}
}

// Regression: Sub resumed only when `level < low`, so landing exactly
// on the low watermark stayed Paused; the boundary must be `<= low`.
func TestResumeAtExactlyLow(t *testing.T) {
	c := mustNew(t, 10, 20, 100)
	c.Add(20) // Paused
	c.Sub(10) // exactly low -> must resume
	if got := c.Stat(); got.State != Flowing || got.Resumes != 1 {
		t.Fatalf("got %+v, want Flowing with 1 resume at level == low", got)
	}
}
