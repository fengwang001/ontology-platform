package ontology

import (
	"sync"
	"testing"
)

func TestCloseIdempotent(t *testing.T) {
	d := NewDispatcher()
	if err := d.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPublishAfterCloseFails(t *testing.T) {
	d := NewDispatcher()
	sub := mustSubscribe(t, d, SubscribeOptions{Prefix: "e", BufferSize: 2})
	mustPublish(t, d, "e1", "p", 1)
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.Publish("e1", "p", 2); err != ErrClosed {
		t.Fatalf("Publish after Close = %v, want ErrClosed", err)
	}
	if got := tryRecvSeqs(sub); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("queued messages after close = %v, want [1]", got)
	}
}

func TestSubscribeAfterCloseFails(t *testing.T) {
	d := NewDispatcher()
	d.Close()
	if _, err := d.Subscribe(SubscribeOptions{BufferSize: 1}); err != ErrClosed {
		t.Fatalf("Subscribe after Close = %v, want ErrClosed", err)
	}
}

func TestCloseDiscardDropsQueued(t *testing.T) {
	d := NewDispatcher()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 4, Drain: DrainDiscard,
	})
	mustPublish(t, d, "e1", "p", 1)
	d.Close()
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("discard drain kept %+v after close", m)
	}
}

// TestCloseAtomicity: every Publish that returned nil must have fully
// delivered before Close took effect; every Publish after must have
// failed. There must be no partial state.
func TestCloseAtomicity(t *testing.T) {
	d := NewDispatcher()
	const total = 500
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: total, Drain: DrainRead,
	})
	var mu sync.Mutex
	var succeeded []uint64
	first := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		once := sync.Once{}
		for i := 0; i < total; i++ {
			err := d.Publish("e1", "p", i)
			if err == ErrClosed {
				return
			}
			if err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
			mu.Lock()
			succeeded = append(succeeded, uint64(i+1))
			mu.Unlock()
			once.Do(func() { close(first) })
		}
	}()
	<-first
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done
	mu.Lock()
	want := append([]uint64(nil), succeeded...)
	mu.Unlock()
	if got := tryRecvSeqs(sub); !equalSeqs(got, want) {
		t.Fatalf("delivered %d messages, %d publishes succeeded", len(got), len(want))
	}
}
