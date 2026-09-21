package backpressure

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, low, high, hard int64) *Controller {
	t.Helper()
	c, err := New(low, high, hard)
	if err != nil {
		t.Fatalf("New(%d, %d, %d) returned error: %v", low, high, hard, err)
	}
	return c
}

// Semantics 1: hysteresis. Pause at >= high, resume only at <= low, and
// no state changes while moving inside (low, high).
func TestHysteresis(t *testing.T) {
	c := mustNew(t, 10, 20, 100)

	c.Add(15) // inside (low, high): no pause
	if got := c.Stat().State; got != Flowing {
		t.Fatalf("level 15 in (low, high): state = %v, want Flowing", got)
	}
	c.Add(5) // reaches high exactly: pause
	if got := c.Stat().State; got != Paused {
		t.Fatalf("level 20 == high: state = %v, want Paused", got)
	}
	c.Sub(5) // back inside (low, high): stay paused
	if got := c.Stat().State; got != Paused {
		t.Fatalf("level 15 in (low, high): state = %v, want Paused", got)
	}
	c.Sub(4) // level 11 > low: stay paused
	if got := c.Stat().State; got != Paused {
		t.Fatalf("level 11 > low: state = %v, want Paused", got)
	}
	c.Sub(1) // level 10 == low: resume
	if got := c.Stat().State; got != Flowing {
		t.Fatalf("level 10 == low: state = %v, want Flowing", got)
	}
	r := c.Stat()
	if r.Pauses != 1 || r.Resumes != 1 {
		t.Fatalf("Pauses=%d Resumes=%d, want 1/1", r.Pauses, r.Resumes)
	}
}

// Semantics 2: each listener fires exactly once per real transition, in
// registration order, and listeners may call Stat without deadlocking.
func TestCallbacksExactlyOnceInOrder(t *testing.T) {
	c := mustNew(t, 5, 10, 50)
	var seq []string
	var statInCallback Report
	c.OnChange(func(s State) {
		seq = append(seq, "first:"+s.String())
		statInCallback = c.Stat() // must not deadlock
	})
	c.OnChange(func(s State) {
		seq = append(seq, "second:"+s.String())
	})

	c.Add(3) // no transition
	c.Add(3) // no transition
	if len(seq) != 0 {
		t.Fatalf("callbacks fired without transition: %v", seq)
	}
	c.Add(4) // level 10: pause
	c.Sub(5) // level 5: resume
	c.Sub(3) // level 2: no transition

	want := []string{"first:Paused", "second:Paused", "first:Flowing", "second:Flowing"}
	if len(seq) != len(want) {
		t.Fatalf("callback sequence = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("callback sequence = %v, want %v", seq, want)
		}
	}
	if statInCallback.State != Flowing || statInCallback.Pauses != 1 {
		t.Fatalf("Stat inside callback = %+v, want State=Flowing Pauses=1", statInCallback)
	}
}

// Semantics 3: hard limit rejects the whole amount; exactly hard is fine.
func TestHardLimit(t *testing.T) {
	c := mustNew(t, 10, 20, 30)
	if !c.Add(30) { // exactly hard: accepted
		t.Fatal("Add(30) to exactly hard was rejected")
	}
	if c.Add(1) { // would exceed: rejected wholesale
		t.Fatal("Add(1) beyond hard was accepted")
	}
	r := c.Stat()
	if r.Level != 30 {
		t.Fatalf("Level = %d, want 30 (no partial accept)", r.Level)
	}
	if r.Rejected != 1 {
		t.Fatalf("Rejected = %d, want 1", r.Rejected)
	}
}

// Semantics 4: rejected Add causes no state change and no counter drift.
func TestRejectedAddKeepsCounters(t *testing.T) {
	c := mustNew(t, 10, 20, 25)
	calls := 0
	c.OnChange(func(State) { calls++ })

	c.Add(15)      // level 15, Flowing
	if c.Add(20) { // 15+20 > 25: rejected
		t.Fatal("overflowing Add was accepted")
	}
	r := c.Stat()
	if r.Level != 15 || r.State != Flowing {
		t.Fatalf("after rejection: Level=%d State=%v, want 15/Flowing", r.Level, r.State)
	}
	if r.Pauses != 0 || r.Resumes != 0 {
		t.Fatalf("Pauses=%d Resumes=%d, want 0/0", r.Pauses, r.Resumes)
	}
	if calls != 0 {
		t.Fatalf("callbacks fired %d times, want 0", calls)
	}
}

// Semantics 5: Sub clamps at zero and resumes based on the clamped value.
func TestSubClampsAtZero(t *testing.T) {
	c := mustNew(t, 10, 20, 100)
	c.Add(25) // paused
	c.Sub(40) // would go negative: clamp to 0, which is <= low, so resume
	r := c.Stat()
	if r.Level != 0 {
		t.Fatalf("Level = %d, want 0 (clamped)", r.Level)
	}
	if r.State != Flowing {
		t.Fatalf("State = %v, want Flowing after clamp to 0", r.State)
	}
	if r.Resumes != 1 {
		t.Fatalf("Resumes = %d, want 1", r.Resumes)
	}
}

// Semantics 6: zero or negative n is invalid for Add and Sub.
func TestNonPositiveAmountsAreNoops(t *testing.T) {
	c := mustNew(t, 10, 20, 100)
	for _, n := range []int64{0, -1, -100} {
		if c.Add(n) {
			t.Fatalf("Add(%d) returned true", n)
		}
		c.Sub(n)
	}
	r := c.Stat()
	if r.Level != 0 || r.Rejected != 0 || r.Pauses != 0 || r.Resumes != 0 {
		t.Fatalf("report after noops = %+v, want all zero", r)
	}
}

// Semantics 7: configuration validation.
func TestNewValidation(t *testing.T) {
	bad := [][3]int64{
		{10, 10, 100}, // low == high
		{20, 10, 100}, // low > high
		{0, 30, 20},   // high > hard
		{-1, 10, 100}, // negative low
		{0, -5, 100},  // negative high
		{0, 10, -100}, // negative hard
		{-1, -1, -1},  // all negative
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
	good := [][3]int64{
		{0, 10, 10},   // low == 0 valid, high == hard valid
		{0, 1, 1},     // minimal
		{10, 20, 100}, // typical
	}
	for _, w := range good {
		if _, err := New(w[0], w[1], w[2]); err != nil {
			t.Errorf("New%v: unexpected error %v", w, err)
		}
	}
}
