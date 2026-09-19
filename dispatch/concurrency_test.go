package dispatch

import (
	"sync"
	"testing"
)

// A slow subscriber (tiny queue, never drained) must not block the
// producer: all publishes complete while its queue stays full. If Publish
// ever blocked on the slow subscriber, this test would hang and fail via
// the test timeout instead of sleeping.
func TestProducerNeverBlockedBySlowSubscriber(t *testing.T) {
	d := New()
	defer d.Close()
	slow := mustSubscribe(t, d, Options{Capacity: 1, OnFull: DropNewest})
	fast := mustSubscribe(t, d, Options{Capacity: 4096, OnFull: DropNewest})

	const n = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := d.Publish("e", "a", i); err != nil {
				t.Errorf("Publish: %v", err)
			}
		}
	}()
	wg.Wait()

	// The fast subscriber received everything; the slow one dropped n-1.
	got := drainAfterCancel(fast)
	if len(got) != n {
		t.Fatalf("fast subscriber received %d, want %d", len(got), n)
	}
	if dropped := slow.Dropped(); dropped != n-1 {
		t.Fatalf("slow Dropped() = %d, want %d", dropped, n-1)
	}
	slow.Cancel()
	drain(slow.C())
}

// Concurrent publishers, subscribers, cancellers, and readers, run under
// -race. Every subscriber that stays subscribed must observe strictly
// increasing seqs, and received-plus-dropped accounting must stay
// consistent.
func TestConcurrentStorm(t *testing.T) {
	d := New()
	const publishers = 8
	const perPublisher = 250

	survivor := mustSubscribe(t, d, Options{Capacity: 64, OnFull: DropOldest})
	cancelled := mustSubscribe(t, d, Options{Capacity: 4, OnFull: Disconnect})

	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				if _, err := d.Publish("e", "a", base+i); err != nil {
					t.Errorf("Publish: %v", err)
				}
			}
		}(p * perPublisher)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		cancelled.Cancel()
	}()
	wg.Wait()

	// The survivor's received seqs must be strictly increasing, and the
	// gap between first and last must equal the number of drops.
	survivor.Cancel()
	got := drain(survivor.C())
	total := uint64(publishers * perPublisher)
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("seqs not strictly increasing at %d: %v...", i, got[maxInt(0, i-3):i+2])
		}
	}
	if len(got) > 0 {
		// All matched messages up to the last received seq are either
		// received or dropped: last - received == dropped.
		if got[len(got)-1]-uint64(len(got)) != survivor.Dropped() {
			t.Fatalf("gap %d != dropped %d",
				got[len(got)-1]-uint64(len(got)), survivor.Dropped())
		}
		if got[len(got)-1] != total {
			t.Fatalf("last seq = %d, want %d", got[len(got)-1], total)
		}
	}
	drain(cancelled.C())
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
