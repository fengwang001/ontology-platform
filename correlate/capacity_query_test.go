package correlate

import (
	"errors"
	"testing"
	"time"

	"ontology/slot"
)

func TestCapacityFailureChangesNothing(t *testing.T) {
	c, _ := newTestCorrelator(t, 2)
	mustRequest(t, c, time.Minute)
	mustRequest(t, c, time.Minute)

	before := c.Counters()
	if _, err := c.Request(time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("err = %v, want ErrCapacity", err)
	}
	if c.InFlight() != 2 {
		t.Fatalf("in flight = %d, want 2", c.InFlight())
	}
	if c.Counters() != before {
		t.Fatalf("counters changed: %+v vs %+v", before, c.Counters())
	}
}

func TestBadBudgetRejected(t *testing.T) {
	c, _ := newTestCorrelator(t, 1)
	if _, err := c.Request(0); !errors.Is(err, ErrBadBudget) {
		t.Fatalf("zero budget err = %v", err)
	}
	if _, err := c.Request(-time.Second); !errors.Is(err, ErrBadBudget) {
		t.Fatalf("negative budget err = %v", err)
	}
	if c.InFlight() != 0 {
		t.Fatal("rejected budget must not open a slot")
	}
}

func TestLookupZeroAndStable(t *testing.T) {
	c, clock := newTestCorrelator(t, 2)
	id := mustRequest(t, c, 10*time.Second)

	info := c.Lookup(id)
	if !info.InFlight || info.State != slot.Pending {
		t.Fatalf("lookup = %+v, want pending", info)
	}
	if info.Remaining != 10*time.Second {
		t.Fatalf("remaining = %v, want 10s", info.Remaining)
	}
	again := c.Lookup(id)
	if again != info {
		t.Fatalf("repeat lookup differs: %+v vs %+v", info, again)
	}

	if err := c.Deliver(id); err != nil {
		t.Fatal(err)
	}
	if got := c.Lookup(id); got != (Info{}) {
		t.Fatalf("settled lookup = %+v, want zero", got)
	}
	if got := c.Lookup(slot.Encode(123, 7)); got != (Info{}) {
		t.Fatalf("unknown lookup = %+v, want zero", got)
	}

	// Query does not advance lazy timeout state.
	clock.advance(20 * time.Second)
	if got := c.Lookup(id); got != (Info{}) {
		t.Fatalf("lookup after reuse window = %+v", got)
	}
}

func TestAllocationSequenceReproducible(t *testing.T) {
	sequence := func() []uint64 {
		c, _ := newTestCorrelator(t, 3)
		var ids []uint64
		for range 3 {
			id := mustRequest(t, c, time.Minute)
			ids = append(ids, uint64(id.Index()))
		}
		c.Deliver(slot.Encode(2, 1))
		c.Deliver(slot.Encode(0, 1))
		reused := mustRequest(t, c, time.Minute) // lowest free = 0
		ids = append(ids, uint64(reused.Index()))
		reused = mustRequest(t, c, time.Minute) // next lowest = 2
		ids = append(ids, uint64(reused.Index()))
		return ids
	}
	want := []uint64{0, 1, 2, 0, 2}
	got := sequence()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
}
