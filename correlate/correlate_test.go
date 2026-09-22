package correlate

import (
	"errors"
	"testing"
	"time"

	"ontology/slot"
)

func TestNormalAndOutOfOrderDelivery(t *testing.T) {
	c, _ := newTestCorrelator(t, 4)
	a := mustRequest(t, c, 10*time.Second)
	b := mustRequest(t, c, 10*time.Second)
	if c.InFlight() != 2 {
		t.Fatalf("in flight = %d, want 2", c.InFlight())
	}
	if err := c.Deliver(b); err != nil { // later request answers first
		t.Fatalf("deliver b: %v", err)
	}
	if err := c.Deliver(a); err != nil {
		t.Fatalf("deliver a: %v", err)
	}
	if c.InFlight() != 0 {
		t.Fatalf("in flight = %d, want 0", c.InFlight())
	}
	if got := c.Counters().Delivered; got != 2 {
		t.Fatalf("delivered = %d, want 2", got)
	}
}

func TestLateResponseNeverHitsNewGeneration(t *testing.T) {
	c, clock := newTestCorrelator(t, 1)

	first := mustRequest(t, c, 10*time.Second) // t=0, budget 10
	clock.advance(10 * time.Second)            // t=10: exactly at deadline
	// A mutating op performs lazy cleanup; canceling an unknown ID changes
	// no state but retires the timed-out slot.
	if err := c.Cancel(slot.Encode(42, 1)); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("cleanup op err = %v, want ErrUnknownID", err)
	}
	clock.advance(1 * time.Second) // t=11: recycled slot reused
	second := mustRequest(t, c, 10*time.Second)
	if first.Index() != second.Index() || first.Generation() == second.Generation() {
		t.Fatalf("expected same recycled slot, new generation: %v vs %v", first, second)
	}

	clock.advance(1 * time.Second) // t=12: first generation's response arrives late
	err := c.Deliver(first)
	if !errors.Is(err, ErrStaleID) {
		t.Fatalf("late response err = %v, want ErrStaleID", err)
	}
	if c.InFlight() != 1 {
		t.Fatalf("in flight after late response = %d, want 1", c.InFlight())
	}
	if err := c.Deliver(second); err != nil {
		t.Fatalf("new generation must still complete: %v", err)
	}
	cnt := c.Counters()
	if cnt.Stale != 1 || cnt.Delivered != 1 || cnt.Unknown != 0 || cnt.Idle != 0 {
		t.Fatalf("counters = %+v", cnt)
	}
}

func TestTimeoutIsLazyAndLeftClosed(t *testing.T) {
	c, clock := newTestCorrelator(t, 1)
	id := mustRequest(t, c, 10*time.Second)
	clock.advance(9 * time.Second)
	if _, err := c.Request(time.Second); !errors.Is(err, ErrCapacity) {
		t.Fatalf("before deadline err = %v, want ErrCapacity", err)
	}
	clock.advance(1 * time.Second) // now == deadline
	err := c.Deliver(id)
	if !errors.Is(err, ErrIdleID) {
		t.Fatalf("at deadline deliver = %v, want ErrIdleID", err)
	}
}

func TestUnknownOrphan(t *testing.T) {
	c, _ := newTestCorrelator(t, 2)
	if err := c.Deliver(slot.Encode(99, 1)); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("err = %v, want ErrUnknownID", err)
	}
	if got := c.Counters().Unknown; got != 1 {
		t.Fatalf("unknown count = %d, want 1", got)
	}
}

func TestIdleOrphanAfterCompletion(t *testing.T) {
	c, _ := newTestCorrelator(t, 2)
	id := mustRequest(t, c, time.Minute)
	if err := c.Deliver(id); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(id); !errors.Is(err, ErrIdleID) {
		t.Fatalf("duplicate err = %v, want ErrIdleID", err)
	}
	if got := c.Counters().Idle; got != 1 {
		t.Fatalf("idle count = %d, want 1", got)
	}
}

func TestCanceledResponseIsOrphan(t *testing.T) {
	c, _ := newTestCorrelator(t, 2)
	id := mustRequest(t, c, time.Minute)
	if err := c.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(id); !errors.Is(err, ErrIdleID) {
		t.Fatalf("after cancel err = %v, want ErrIdleID", err)
	}
	if err := c.Cancel(id); !errors.Is(err, ErrNotInFlight) {
		t.Fatalf("double cancel err = %v, want ErrNotInFlight", err)
	}
}
