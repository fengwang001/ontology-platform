package policy

import (
	"errors"
	"runtime"
	"testing"
)

func TestRetryBoundaryLeftClosedRightOpen(t *testing.T) {
	p := Policy{MaxRetries: 2, IsRetryable: func(error) bool { return true }}
	// totalSoFar counts attempts already performed.
	cases := []struct {
		done int
		want bool
	}{
		{0, true},  // after attempt 1 -> retry 1 allowed
		{1, true},  // after attempt 2 -> retry 2 allowed
		{2, false}, // after attempt 3 -> terminal (N+1)
		{3, false},
	}
	for _, c := range cases {
		if got := p.RetryAllowed(c.done, errors.New("e")); got != c.want {
			t.Fatalf("done=%d got %v want %v", c.done, got, c.want)
		}
	}
}

func TestNonRetryableFailsImmediately(t *testing.T) {
	p := Policy{MaxRetries: 5} // IsRetryable nil => never retry
	if p.RetryAllowed(0, errors.New("e")) {
		t.Fatal("nil retryable predicate must deny all")
	}
}

func TestBackoffIndexedByAttempt(t *testing.T) {
	p := Policy{BackoffMillis: []int64{10, 20, 30}}
	if p.Backoff(2) != 10 || p.Backoff(3) != 20 || p.Backoff(4) != 30 {
		t.Fatal("backoff indexing wrong")
	}
	// Beyond slice: reuse last element.
	if p.Backoff(9) != 30 {
		t.Fatal("expected last-element reuse")
	}
}

func TestManualClockAdvanceReleasesSleep(t *testing.T) {
	c := NewManualClock()
	woke := make(chan struct{})
	go func() {
		c.Sleep(100)
		close(woke)
	}()
	runtime.Gosched()
	if c.Pending() != 1 {
		t.Fatal("sleeper not registered")
	}
	c.Advance(99)
	select {
	case <-woke:
		t.Fatal("slept too early")
	default:
	}
	c.Advance(1)
	<-woke
}
