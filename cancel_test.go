package ontology

import (
	"sync"
	"testing"
)

func TestCancelIdempotent(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{ID: "s", Prefix: "e", BufferSize: 2})
	sub.Cancel()
	sub.Cancel()
	sub.Cancel()
	if ids := d.Match("e1", "p"); len(ids) != 0 {
		t.Fatalf("cancelled subscription still matched: %v", ids)
	}
}

func TestCancelDiscardDropsQueued(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 4, Drain: DrainDiscard,
	})
	mustPublish(t, d, "e1", "p", 1)
	mustPublish(t, d, "e1", "p", 2)
	sub.Cancel()
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("discard drain kept %+v after cancel", m)
	}
	mustPublish(t, d, "e1", "p", 3)
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("received %+v after cancel", m)
	}
}

func TestCancelDrainReadKeepsQueued(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 4, Drain: DrainRead,
	})
	mustPublish(t, d, "e1", "p", 1)
	mustPublish(t, d, "e1", "p", 2)
	sub.Cancel()
	if got := tryRecvSeqs(sub); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("drain-read after cancel got %v, want [1 2]", got)
	}
	mustPublish(t, d, "e1", "p", 3)
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("received %+v after cancel", m)
	}
	if _, ok := sub.Receive(); ok {
		t.Fatal("Receive on drained cancelled subscription must fail")
	}
}

func TestConcurrentCancel(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{ID: "s", Prefix: "e", BufferSize: 2})
	mustPublish(t, d, "e1", "p", 1)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub.Cancel()
		}()
	}
	wg.Wait()
}

func TestCancelDuringPublishDoesNotBreakOthers(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	const total = 500
	victim := mustSubscribe(t, d, SubscribeOptions{
		ID: "victim", Prefix: "e", BufferSize: 2, Full: FullDropNewest,
	})
	other := mustSubscribe(t, d, SubscribeOptions{
		ID: "other", Prefix: "e", BufferSize: total,
	})
	cancelled := make(chan struct{})
	go func() {
		defer close(cancelled)
		for i := 0; i < total; i++ {
			if err := d.Publish("e1", "p", i); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
	}()
	victim.Cancel()
	<-cancelled
	victim.Cancel()
	if got := tryRecvSeqs(other); len(got) != total {
		t.Fatalf("other subscriber got %d of %d messages", len(got), total)
	}
	if dropped, _ := other.Dropped(); dropped != 0 {
		t.Fatalf("other subscriber dropped %d", dropped)
	}
}
