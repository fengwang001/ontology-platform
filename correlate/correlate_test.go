package correlate

import (
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestCorrelator(capacity int) (*Correlator, *fakeClock) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	return New(capacity, clk.now), clk
}

func TestNormalAndOutOfOrderDelivery(t *testing.T) {
	cor, clk := newTestCorrelator(4)
	t1, err := cor.Issue(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := cor.Issue(10 * time.Second)
	t3, _ := cor.Issue(10 * time.Second)
	if got := cor.InFlight(); got != 3 {
		t.Fatalf("inflight = %d, want 3", got)
	}
	// Out of order: third reply arrives first.
	if err := cor.Deliver(t3); err != nil {
		t.Fatalf("deliver t3: %v", err)
	}
	if err := cor.Deliver(t1); err != nil {
		t.Fatalf("deliver t1: %v", err)
	}
	if err := cor.Deliver(t2); err != nil {
		t.Fatalf("deliver t2: %v", err)
	}
	if got := cor.InFlight(); got != 0 {
		t.Fatalf("inflight after all = %d, want 0", got)
	}
	if got := cor.Counters().Completed; got != 3 {
		t.Fatalf("completed = %d, want 3", got)
	}
	clk.advance(time.Second) // time passing cannot resurrect anything
}

func TestExactlyOnceDelivery(t *testing.T) {
	cor, _ := newTestCorrelator(2)
	tok, _ := cor.Issue(time.Minute)
	if err := cor.Deliver(tok); err != nil {
		t.Fatal(err)
	}
	// Second delivery of the same reply: ID is idle after a finished gen.
	if err := cor.Deliver(tok); !errors.Is(err, ErrOrphanIdle) {
		t.Fatalf("duplicate delivery err = %v, want ErrOrphanIdle", err)
	}
	if got := cor.Counters().Completed; got != 1 {
		t.Fatalf("completed = %d, want exactly 1", got)
	}
}

// Reproduces the scenario from the specification, tick by tick.
func TestLateReplyNeverBindsToReusedID(t *testing.T) {
	cor, clk := newTestCorrelator(1)

	first, err := cor.Issue(10 * time.Second) // t=0, deadline t=10
	if err != nil || first.ID != 0 || first.Epoch != 1 {
		t.Fatalf("first issue = %+v,%v", first, err)
	}

	clk.advance(10 * time.Second) // t=10: deadline reached
	if _, ok := cor.Lookup(first.ID); ok {
		t.Fatal("request must be timed out at t == deadline")
	}
	if got := cor.Counters().TimedOut; got != 1 {
		t.Fatalf("timed out = %d, want 1", got)
	}

	clk.advance(time.Second) // t=11
	second, err := cor.Issue(10 * time.Second)
	if err != nil || second.ID != 0 || second.Epoch != 2 {
		t.Fatalf("reused issue = %+v,%v", second, err)
	}

	clk.advance(time.Second) // t=12: late reply of the first generation
	err = cor.Deliver(first)
	if !errors.Is(err, ErrOrphanStale) {
		t.Fatalf("late reply err = %v, want ErrOrphanStale", err)
	}

	st, ok := cor.Lookup(second.ID)
	if !ok || st.Epoch != 2 || !st.Waiting {
		t.Fatalf("newer request must stay in flight: %+v,%v", st, ok)
	}
	if got := cor.Counters().OrphanStale; got != 1 {
		t.Fatalf("orphan stale = %d, want 1", got)
	}
	if got := cor.Counters().Completed; got != 0 {
		t.Fatalf("completed = %d, want 0", got)
	}
}
