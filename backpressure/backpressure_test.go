package backpressure

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, low, high, hard int64) *Controller {
	t.Helper()
	c, err := New(low, high, hard)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", low, high, hard, err)
	}
	return c
}

// Semantics 1: hysteresis. Pause at >= high, resume only at <= low,
// no transitions while moving inside (low, high).
func TestHysteresis(t *testing.T) {
	c := mustNew(t, 10, 20, 100)
	if !c.Add(19) {
		t.Fatal("Add(19) should be accepted")
	}
	if got := c.Stat().State; got != Flowing {
		t.Fatalf("level 19 below high: state = %v, want Flowing", got)
	}
	c.Add(1) // level 20 == high -> pause
	if got := c.Stat(); got.State != Paused || got.Pauses != 1 {
		t.Fatalf("level 20 at high: got %+v, want Paused with Pauses=1", got)
	}
	// Move inside (low, high): no transition either direction.
	c.Sub(5) // 15
	c.Add(4) // 19
	c.Sub(8) // 11
	c.Add(8) // 19
	if got := c.Stat(); got.State != Paused || got.Pauses != 1 || got.Resumes != 0 {
		t.Fatalf("oscillating in (low,high): got %+v, want Paused Pauses=1 Resumes=0", got)
	}
	c.Sub(8) // 11, still above low
	if got := c.Stat().State; got != Paused {
		t.Fatalf("level 11 above low: state = %v, want Paused", got)
	}
	c.Sub(1) // 10 == low -> resume
	if got := c.Stat(); got.State != Flowing || got.Resumes != 1 {
		t.Fatalf("level 10 at low: got %+v, want Flowing with Resumes=1", got)
	}
}

// Semantics 2: callbacks fire exactly once per real transition,
// in registration order, and may call Stat without deadlocking.
func TestCallbacksExactlyOnceInOrder(t *testing.T) {
	c := mustNew(t, 5, 10, 100)
	var log []string
	c.OnChange(func(s State) {
		r := c.Stat() // must not deadlock
		if r.State != s {
			t.Errorf("callback state %v, Stat says %v", s, r.State)
		}
		log = append(log, "a:"+s.String())
	})
	c.OnChange(func(s State) {
		log = append(log, "b:"+s.String())
	})
	c.Add(3) // no transition
	c.Add(7) // pause
	c.Add(1) // still paused, no transition
	c.Sub(4) // 7, still paused
	c.Sub(2) // 5 == low, resume
	want := []string{"a:Paused", "b:Paused", "a:Flowing", "b:Flowing"}
	if len(log) != len(want) {
		t.Fatalf("callback log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("callback log = %v, want %v", log, want)
		}
	}
}

// Semantics 3+4: hard limit rejects the whole amount, level untouched,
// Rejected accumulates, exactly-hard is accepted, no counters change.
func TestHardLimit(t *testing.T) {
	c := mustNew(t, 10, 20, 50)
	c.Add(40)
	before := c.Stat()
	if c.Add(11) {
		t.Fatal("Add(11) over hard limit should be rejected")
	}
	r := c.Stat()
	if r.Level != before.Level || r.Rejected != before.Rejected+11 {
		t.Fatalf("after rejection: got %+v, want Level=40 Rejected=%d", r, before.Rejected+11)
	}
	if r.Pauses != before.Pauses || r.Resumes != before.Resumes || r.State != before.State {
		t.Fatalf("rejection must not change state/counters: got %+v", r)
	}
	if !c.Add(10) { // exactly hard
		t.Fatal("Add landing exactly on hard should be accepted")
	}
	if got := c.Stat().Level; got != 50 {
		t.Fatalf("level = %d, want 50", got)
	}
	if c.Add(1) {
		t.Fatal("Add past hard should be rejected")
	}
	if got := c.Stat(); got.Level != 50 || got.Rejected != 12 {
		t.Fatalf("got %+v, want Level=50 Rejected=12", got)
	}
}

// Semantics 5: Sub clamps at 0 and resume uses the clamped value.
func TestSubClampsAtZero(t *testing.T) {
	c := mustNew(t, 4, 10, 100)
	c.Add(12) // paused at 12
	c.Sub(9)  // 3 <= low -> resume
	if got := c.Stat(); got.State != Flowing || got.Level != 3 {
		t.Fatalf("got %+v, want Flowing Level=3", got)
	}
	c.Sub(100) // clamps to 0
	if got := c.Stat().Level; got != 0 {
		t.Fatalf("level = %d, want 0 after clamp", got)
	}
	// Clamp-driven resume: paused, huge Sub clamps to 0 <= low.
	c2 := mustNew(t, 0, 10, 100)
	c2.Add(10) // paused
	c2.Sub(50) // clamps to 0 == low -> resume
	if got := c2.Stat(); got.State != Flowing || got.Level != 0 || got.Resumes != 1 {
		t.Fatalf("got %+v, want Flowing Level=0 Resumes=1", got)
	}
}

// Semantics 6: zero or negative n is invalid for Add and Sub.
func TestNonPositiveAmountsInvalid(t *testing.T) {
	c := mustNew(t, 5, 10, 100)
	c.Add(7)
	before := c.Stat()
	for _, n := range []int64{0, -1, -100} {
		if c.Add(n) {
			t.Errorf("Add(%d) should return false", n)
		}
		c.Sub(n)
	}
	if got := c.Stat(); got != before {
		t.Fatalf("non-positive amounts changed state: before %+v after %+v", before, got)
	}
}

// Semantics 7: configuration validation.
func TestConfigValidation(t *testing.T) {
	bad := [][3]int64{
		{5, 5, 10},   // low == high
		{6, 5, 10},   // low > high
		{1, 11, 10},  // high > hard
		{-1, 5, 10},  // negative low
		{1, -5, 10},  // negative high
		{1, 5, -10},  // negative hard
		{-1, -1, -1}, // all negative
	}
	for _, w := range bad {
		c, err := New(w[0], w[1], w[2])
		if !errors.Is(err, ErrBadWatermark) {
			t.Errorf("New%v: err = %v, want ErrBadWatermark", w, err)
		}
		if c != nil {
			t.Errorf("New%v: controller = %v, want nil", w, c)
		}
	}
	c, err := New(0, 1, 1) // low == 0 is legal
	if err != nil || c == nil {
		t.Errorf("New(0,1,1) = (%v, %v), want valid controller", c, err)
	}
}
