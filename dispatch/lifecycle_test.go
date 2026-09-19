package dispatch

import (
	"errors"
	"sync"
	"testing"
)

// TestPublishAfterClose: Publish fails with a detectable ErrClosed and
// delivers nothing.
func TestPublishAfterClose(t *testing.T) {
	d := New()
	s := mustSubscribe(t, d, SubscribeOptions{
		ID: "s", EntityPrefix: "e", BufferSize: 2, DrainOnCancel: true,
	})
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Publish("e1", "p", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after Close = %v, want ErrClosed", err)
	}
	if got := drainClosed(s); len(got) != 0 {
		t.Fatalf("delivered %d messages after close, want 0", len(got))
	}
}

// TestSubscribeAfterClose: subscribing after Close fails with ErrClosed.
func TestSubscribeAfterClose(t *testing.T) {
	d := New()
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := d.Subscribe(SubscribeOptions{ID: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after Close = %v, want ErrClosed", err)
	}
}

// TestCloseIdempotent: Close may be called any number of times.
func TestCloseIdempotent(t *testing.T) {
	d := New()
	for i := 0; i < 3; i++ {
		if err := d.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

// TestCloseFinishesQueues: on Close, each subscriber's queue is finished
// per its DrainOnCancel setting.
func TestCloseFinishesQueues(t *testing.T) {
	d := New()
	drainer := mustSubscribe(t, d, SubscribeOptions{
		ID: "drain", EntityPrefix: "e", BufferSize: 4, DrainOnCancel: true,
	})
	discarder := mustSubscribe(t, d, SubscribeOptions{
		ID: "discard", EntityPrefix: "e", BufferSize: 4,
	})
	mustPublish(t, d, "e1", "p", 1)
	mustPublish(t, d, "e1", "p", 2)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := drainClosed(drainer); !equalSeqs(seqsOf(got), []uint64{1, 2}) {
		t.Fatalf("drainer got %v, want [1 2]", seqsOf(got))
	}
	if got := drainClosed(discarder); len(got) != 0 {
		t.Fatalf("discarder got %d messages, want 0", len(got))
	}
	if discarder.Dropped() != 2 {
		t.Fatalf("discarder Dropped() = %d, want 2", discarder.Dropped())
	}
	select {
	case <-drainer.Done():
	default:
		t.Fatal("drainer Done() not closed")
	}
}

// TestCloseAtomicity: a Publish racing with Close either completes for all
// subscribers or for none — never for a subset.
func TestCloseAtomicity(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		d := New()
		a := mustSubscribe(t, d, SubscribeOptions{
			ID: "a", EntityPrefix: "e", BufferSize: 1, DrainOnCancel: true,
		})
		b := mustSubscribe(t, d, SubscribeOptions{
			ID: "b", EntityPrefix: "e", BufferSize: 1, DrainOnCancel: true,
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
			_ = d.Close()
		}()
		close(start)
		wg.Wait()
		aGot := drainClosed(a)
		bGot := drainClosed(b)
		if pubErr == nil {
			if len(aGot) != 1 || len(bGot) != 1 {
				t.Fatalf("iter %d: Publish succeeded but a=%d b=%d messages",
					iter, len(aGot), len(bGot))
			}
		} else {
			if !errors.Is(pubErr, ErrClosed) {
				t.Fatalf("iter %d: Publish error = %v, want ErrClosed", iter, pubErr)
			}
			if len(aGot) != 0 || len(bGot) != 0 {
				t.Fatalf("iter %d: Publish failed but a=%d b=%d messages",
					iter, len(aGot), len(bGot))
			}
		}
	}
}
