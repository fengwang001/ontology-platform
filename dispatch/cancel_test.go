package dispatch

import (
	"sync"
	"testing"
)

// TestCancelIdempotent: repeated cancellation neither panics nor errors.
func TestCancelIdempotent(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{ID: "s", EntityPrefix: "e"})
	s.Cancel()
	s.Cancel()
	s.Cancel()
	select {
	case <-s.Done():
	default:
		t.Fatal("Done() not closed after Cancel")
	}
}

// TestCancelStopsDelivery: after Cancel, no further messages arrive.
func TestCancelStopsDelivery(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "s", EntityPrefix: "e", BufferSize: 4,
	})
	mustPublish(t, d, "e1", "p", 1)
	s.Cancel()
	mustPublish(t, d, "e1", "p", 2)
	// DrainOnCancel=false: the buffered message is discarded and counted.
	if got := drainClosed(s); len(got) != 0 {
		t.Fatalf("received %d messages after cancel, want 0", len(got))
	}
	if s.Dropped() != 1 || s.LastDropSeq() != 1 {
		t.Fatalf("Dropped=%d LastDropSeq=%d, want 1 and 1",
			s.Dropped(), s.LastDropSeq())
	}
}

// TestCancelDrain: with DrainOnCancel, buffered messages remain readable.
func TestCancelDrain(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "s", EntityPrefix: "e", BufferSize: 4, DrainOnCancel: true,
	})
	mustPublish(t, d, "e1", "p", 1)
	mustPublish(t, d, "e1", "p", 2)
	s.Cancel()
	mustPublish(t, d, "e1", "p", 3) // must not be delivered
	got := drainClosed(s)
	if !equalSeqs(seqsOf(got), []uint64{1, 2}) {
		t.Fatalf("drained seqs %v, want [1 2]", seqsOf(got))
	}
	if s.Dropped() != 0 {
		t.Fatalf("Dropped() = %d, want 0", s.Dropped())
	}
}

// TestConcurrentCancel: many goroutines cancelling the same subscriber is
// safe and exactly one of them observes the removal.
func TestConcurrentCancel(t *testing.T) {
	d := New()
	defer d.Close()
	s := mustSubscribe(t, d, SubscribeOptions{ID: "s", EntityPrefix: "e"})
	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s.Cancel()
		}()
	}
	wg.Wait()
	select {
	case <-s.Done():
	default:
		t.Fatal("Done() not closed after concurrent cancels")
	}
	if ids := d.Match("e1", "p"); len(ids) != 0 {
		t.Fatalf("cancelled subscriber still matched: %v", ids)
	}
}

// TestCancelDuringPublish: cancelling a subscriber while a Publish fan-out
// is in progress must not fail the Publish nor starve other subscribers.
func TestCancelDuringPublish(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		d := New()
		victim := mustSubscribe(t, d, SubscribeOptions{
			ID: "victim", EntityPrefix: "e", BufferSize: 1,
		})
		bystander := mustSubscribe(t, d, SubscribeOptions{
			ID: "bystander", EntityPrefix: "e", BufferSize: 1, DrainOnCancel: true,
		})
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var pubErr error
		go func() {
			defer wg.Done()
			<-start
			pubErr = d.Publish("e1", "p", iter)
		}()
		go func() {
			defer wg.Done()
			<-start
			victim.Cancel()
		}()
		close(start)
		wg.Wait()
		if pubErr != nil {
			t.Fatalf("iter %d: Publish returned %v", iter, pubErr)
		}
		// The bystander must receive the message regardless of the race.
		got := recvN(t, bystander, 1)
		if got[0].Value != iter {
			t.Fatalf("iter %d: bystander got %v, want %v", iter, got[0].Value, iter)
		}
		d.Close()
	}
}
