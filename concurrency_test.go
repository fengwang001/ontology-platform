package ontology

import (
	"errors"
	"io"
	"sync"
	"testing"
)

// A never-reading subscriber with buffer 1 must not block producers:
// thousands of publishes complete promptly while the subscriber accumulates
// drops. Completion is synchronized via a channel, never time.Sleep.
func TestSlowSubscriberDoesNotBlockProducer(t *testing.T) {
	d := NewDispatcher()
	slow, _ := d.Subscribe(SubscribeOptions{Buffer: 1, OnOverflow: DropOldest})
	fast, _ := d.Subscribe(SubscribeOptions{Buffer: 1, OnOverflow: DropNewestAndDisconnect})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20000; i++ {
			if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
				panic(err)
			}
		}
	}()
	<-done // would block here forever if a slow subscriber blocked fan-out

	if slow.DroppedTotal() != 19999 {
		t.Fatalf("slow dropped = %d, want 19999", slow.DroppedTotal())
	}
	if fast.Active() {
		t.Fatal("disconnect-policy subscriber should have been removed")
	}
	if fast.DroppedTotal() != 1 {
		t.Fatalf("fast dropped = %d, want 1", fast.DroppedTotal())
	}
	d.Close()
}

// Race test: many producers, many distinct subscribers, concurrent
// unsubscribes. All per-subscriber sequences must be strictly increasing.
func TestConcurrentFanout(t *testing.T) {
	d := NewDispatcher()
	const subN, prodN, perProd = 6, 6, 300
	subs := make([]*Subscription, subN)
	for i := range subs {
		subs[i], _ = d.Subscribe(SubscribeOptions{
			Buffer:       8,
			OnOverflow:   DropOldest,
			OnCancel:     CancelDrain,
			EntityPrefix: "e",
		})
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < prodN; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perProd; i++ {
				d.Publish(Change{Entity: "e", Attribute: "a"})
			}
		}()
	}
	// One consumer drains every subscriber and checks ordering.
	consumeDone := make(chan struct{})
	var orderErr error
	var cwg sync.WaitGroup
	for _, s := range subs {
		cwg.Add(1)
		go func(s *Subscription) {
			defer cwg.Done()
			var prev int64
			for {
				dl, err := s.Receive()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						orderErr = err
					}
					return
				}
				if dl.Seq <= prev {
					orderErr = errors.New("non-increasing sequence")
					return
				}
				prev = dl.Seq
			}
		}(s)
	}
	go func() { cwg.Wait(); close(consumeDone) }()

	close(start)
	wg.Wait()
	d.Close()
	<-consumeDone
	if orderErr != nil {
		t.Fatal(orderErr)
	}
}
