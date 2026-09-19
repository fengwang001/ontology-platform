package dispatch

import (
	"sync"
	"testing"
)

// After Cancel no further messages arrive; a Drain subscriber may still read
// out what was already queued.
func TestCancelStopsDelivery(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 8, OnFull: DropNewest, OnCancel: Drain})

	publishN(t, d, "e", "a", 3)
	s.Cancel()
	publishN(t, d, "e", "a", 2) // must not be delivered

	assertSeqs(t, drain(s.C()), []uint64{1, 2, 3})
	assertCounters(t, s, 0, 0)
}

// DiscardPending drops queued-but-unread messages on Cancel and counts them.
func TestCancelDiscardPending(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 8, OnFull: DropNewest, OnCancel: DiscardPending})

	publishN(t, d, "e", "a", 3)
	s.Cancel()

	if got := drain(s.C()); len(got) != 0 {
		t.Fatalf("received %v after discard-cancel, want nothing", got)
	}
	assertCounters(t, s, 3, 3)
}

// Cancel is idempotent: repeated calls never panic and never error.
func TestCancelIdempotent(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 1, OnFull: DropNewest})

	s.Cancel()
	s.Cancel()
	s.Cancel()
	drain(s.C())
	assertCounters(t, s, 0, 0)
}

// Concurrent cancels from many goroutines are safe and converge.
func TestCancelConcurrent(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, Options{Capacity: 4, OnFull: DropNewest})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Cancel()
		}()
	}
	wg.Wait()
	drain(s.C())
}

// Cancelling a subscriber while a Publish is mid-fan-out neither fails that
// Publish nor drops delivery to the other subscribers.
func TestCancelDuringPublish(t *testing.T) {
	d := New()
	defer d.Close()
	victim := mustSubscribe(t, d, Options{Capacity: 4, OnFull: DropNewest})
	bystander := mustSubscribe(t, d, Options{Capacity: 256, OnFull: DropNewest})

	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := d.Publish("e", "a", i); err != nil {
				t.Errorf("Publish: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		victim.Cancel()
	}()
	wg.Wait()

	// The bystander must have received every message, in order.
	bystander.Cancel()
	got := drain(bystander.C())
	if len(got) != n {
		t.Fatalf("bystander received %d messages, want %d", len(got), n)
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("bystander seqs not 1..%d in order: %v...", n, got[:8])
		}
	}
	victim.Cancel() // still safe after everything
	drain(victim.C())
}
