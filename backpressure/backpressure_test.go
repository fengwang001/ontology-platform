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

// Semantic 1: hysteresis. Pause at >= high, resume only at <= low,
// and no transitions while bouncing inside (low, high).
func TestHysteresis(t *testing.T) {
	c := mustNew(t, 10, 20, 100)

	if !c.Add(19) {
		t.Fatal("Add(19) rejected")
	}
	if got := c.Stat(); got.State != Flowing || got.Pauses != 0 {
		t.Fatalf("level 19: got %+v, want Flowing with 0 pauses", got)
	}

	// Bounce inside (low, high): no transitions allowed.
	c.Sub(5) // 14
	c.Add(4) // 18
	c.Sub(3) // 15
	if got := c.Stat(); got.State != Flowing || got.Pauses != 0 || got.Resumes != 0 {
		t.Fatalf("bounce in (low,high): got %+v, want no transitions", got)
	}

	c.Add(5) // 20 == high -> Paused
	if got := c.Stat(); got.State != Paused || got.Pauses != 1 {
		t.Fatalf("level 20: got %+v, want Paused with 1 pause", got)
	}

	// Still above low: must stay paused.
	c.Sub(5) // 15
	c.Add(3) // 18
	c.Sub(7) // 11
	if got := c.Stat(); got.State != Paused || got.Resumes != 0 {
		t.Fatalf("level 11 > low: got %+v, want still Paused", got)
	}

	c.Sub(1) // 10 == low -> Flowing
	if got := c.Stat(); got.State != Flowing || got.Resumes != 1 {
		t.Fatalf("level 10: got %+v, want Flowing with 1 resume", got)
	}
}

// Semantic 2: each real transition fires every callback exactly once,
// in registration order; no calls without a transition; Stat inside a
// callback must not deadlock.
func TestCallbacksExactlyOnce(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	var log []string
	mk := func(name string) func(State) {
		return func(s State) {
			r := c.Stat() // must not deadlock
			log = append(log, name+":"+s.String()+":"+r.State.String())
		}
	}
	c.OnChange(mk("a"))
	c.OnChange(mk("b"))

	c.Add(1) // 1, no transition
	c.Add(3) // 4 >= high -> Paused
	c.Add(1) // 5, already paused, no transition
	c.Sub(1) // 4, still paused
	c.Sub(2) // 2 <= low -> Flowing
	c.Sub(1) // 1, already flowing, no transition

	want := []string{"a:Paused:Paused", "b:Paused:Paused", "a:Flowing:Flowing", "b:Flowing:Flowing"}
	if len(log) != len(want) {
		t.Fatalf("callback log %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("callback log %v, want %v", log, want)
		}
	}
}

// Semantic 3: hard limit rejects the whole amount, never partially;
// exactly reaching hard is accepted.
func TestHardLimit(t *testing.T) {
	c := mustNew(t, 2, 4, 10)
	c.Add(8)      // 8
	if c.Add(3) { // 8+3 > 10 -> reject whole
		t.Fatal("Add(3) over hard limit accepted")
	}
	if got := c.Stat(); got.Level != 8 || got.Rejected != 3 {
		t.Fatalf("after reject: got %+v, want Level 8 Rejected 3", got)
	}
	if !c.Add(2) { // 8+2 == 10 == hard -> accept
		t.Fatal("Add(2) reaching exactly hard rejected")
	}
	if got := c.Stat(); got.Level != 10 || got.Rejected != 3 {
		t.Fatalf("at hard: got %+v, want Level 10 Rejected 3", got)
	}
	if c.Add(1) {
		t.Fatal("Add(1) beyond hard accepted")
	}
	if got := c.Stat(); got.Level != 10 || got.Rejected != 4 {
		t.Fatalf("beyond hard: got %+v, want Level 10 Rejected 4", got)
	}
}

// Semantic 4: Pauses/Resumes count only real transitions; rejected
// Adds change neither the state nor the counters.
func TestCountsOnlyRealTransitions(t *testing.T) {
	c := mustNew(t, 2, 4, 5)
	c.Add(4) // Paused
	c.Add(5) // rejected (4+5 > 5)
	c.Add(1) // accepted (4+1 == 5), still Paused
	if got := c.Stat(); got.Pauses != 1 || got.Resumes != 0 || got.Rejected != 5 {
		t.Fatalf("got %+v, want Pauses 1 Resumes 0 Rejected 5", got)
	}
	for i := 0; i < 3; i++ {
		c.Sub(3) // to 2 -> Flowing
		c.Add(3) // to 5 -> Paused
	}
	if got := c.Stat(); got.Pauses != 4 || got.Resumes != 3 {
		t.Fatalf("got %+v, want Pauses 4 Resumes 3", got)
	}
}

// Semantic 7: invalid configurations return ErrBadWatermark and a nil
// controller; low == 0 is legal.
func TestConfigValidation(t *testing.T) {
	bad := [][3]int64{
		{5, 5, 10},  // low == high
		{6, 5, 10},  // low > high
		{1, 11, 10}, // high > hard
		{-1, 5, 10}, // negative low
		{1, -5, 10}, // negative high
		{1, 5, -10}, // negative hard
		{0, 0, 0},   // low == high == hard
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
	for _, w := range [][3]int64{{0, 1, 1}, {0, 5, 5}, {3, 5, 10}} {
		if _, err := New(w[0], w[1], w[2]); err != nil {
			t.Errorf("New%v: unexpected error %v", w, err)
		}
	}
}
