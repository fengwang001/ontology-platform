package dispatch

import (
	"sync"
	"testing"
)

// After Close, Publish fails with ErrClosed and delivers nothing; Subscribe
// fails too.
func TestCloseRejectsPublishAndSubscribe(t *testing.T) {
	d := New()
	s := mustSubscribe(t, d, Options{Capacity: 4, OnFull: DropNewest})

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := d.Publish("e", "a", nil); err != ErrClosed {
		t.Fatalf("Publish after Close err = %v, want ErrClosed", err)
	}
	if _, err := d.Subscribe(Options{Capacity: 1}); err != ErrClosed {
		t.Fatalf("Subscribe after Close err = %v, want ErrClosed", err)
	}
	if got := drain(s.C()); len(got) != 0 {
		t.Fatalf("received %v after Close, want nothing", got)
	}
}

// Close is idempotent.
func TestCloseIdempotent(t *testing.T) {
	d := New()
	for i := 0; i < 3; i++ {
		if err := d.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

// Close finishes each subscription per its declared PendingPolicy: Drain
// subscribers keep their queued messages readable, DiscardPending
// subscribers lose them (counted as drops).
func TestCloseAppliesPendingPolicy(t *testing.T) {
	d := New()
	drainer := mustSubscribe(t, d, Options{Capacity: 8, OnFull: DropNewest, OnCancel: Drain})
	discarder := mustSubscribe(t, d, Options{Capacity: 8, OnFull: DropNewest, OnCancel: DiscardPending})

	publishN(t, d, "e", "a", 3)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertSeqs(t, drain(drainer.C()), []uint64{1, 2, 3})
	assertCounters(t, drainer, 0, 0)

	if got := drain(discarder.C()); len(got) != 0 {
		t.Fatalf("discarder received %v after Close, want nothing", got)
	}
	assertCounters(t, discarder, 3, 3)
}

// A Publish racing with Close either completes its full fan-out or delivers
// nothing at all; it never reaches only some subscribers.
func TestCloseAtomicAgainstPublish(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		d := New()
		s1 := mustSubscribe(t, d, Options{Capacity: 1, OnFull: DropNewest, OnCancel: Drain})
		s2 := mustSubscribe(t, d, Options{Capacity: 1, OnFull: DropNewest, OnCancel: Drain})

		var wg sync.WaitGroup
		wg.Add(2)
		var pubErr error
		go func() {
			defer wg.Done()
			_, pubErr = d.Publish("e", "a", nil)
		}()
		go func() {
			defer wg.Done()
			d.Close()
		}()
		wg.Wait()

		got1 := drain(s1.C())
		got2 := drain(s2.C())
		switch {
		case pubErr == ErrClosed:
			if len(got1) != 0 || len(got2) != 0 {
				t.Fatalf("trial %d: rejected Publish delivered to %d/%d subs", trial, len(got1), len(got2))
			}
		case pubErr == nil:
			if len(got1) != 1 || len(got2) != 1 {
				t.Fatalf("trial %d: accepted Publish reached %d/%d subs, want both", trial, len(got1), len(got2))
			}
		default:
			t.Fatalf("trial %d: unexpected Publish error %v", trial, pubErr)
		}
	}
}
