package ontology

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestUnsubscribeStopsDelivery(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 10, DrainOnUnsubscribe: true})

	if _, err := d.Publish(context.Background(), "u/1", "p", 1, nil); err != nil {
		t.Fatal(err)
	}
	sub.Unsubscribe()
	if _, err := d.Publish(context.Background(), "u/1", "p", 2, nil); err != nil {
		t.Fatal(err)
	}

	ev, err := sub.Next(context.Background())
	if err != nil || ev.Value != 1 {
		t.Fatalf("first read = (%v,%v), want buffered event value 1", ev.Value, err)
	}
	if _, err := sub.Next(context.Background()); !errors.Is(err, ErrDrained) {
		t.Fatalf("second read = %v, want ErrDrained", err)
	}
}

func TestUnsubscribeWithoutDrainDiscardsBuffer(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 10})
	publishN(t, d, 3)
	sub.Unsubscribe()
	if _, err := sub.Next(context.Background()); !errors.Is(err, ErrSubscriptionGone) {
		t.Fatalf("err = %v, want ErrSubscriptionGone", err)
	}
	if st := sub.Stats(); st.Pending != 0 {
		t.Fatalf("pending = %d, want 0", st.Pending)
	}
}

func TestUnsubscribeIdempotentAndByDispatcher(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 10})
	id := sub.ID()
	for i := 0; i < 5; i++ {
		sub.Unsubscribe()
		d.Unsubscribe(id)
		d.Unsubscribe(id + 999)
	}
}

// A subscriber unsubscribing in the middle of a fan-out must not make the
// Publish fail and must not suppress delivery to its peers. All goroutines
// synchronize on a WaitGroup; no sleeps anywhere.
func TestConcurrentUnsubscribeDuringFanout(t *testing.T) {
	d := New()
	defer d.Close()

	const n = 64
	var subs []*Subscription
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sub, _ := d.Subscribe("u", nil, Options{Capacity: 1, Policy: DropNewest})
		subs = append(subs, sub)
	}

	wg.Add(n * 2)
	for _, sub := range subs {
		go func(s *Subscription) {
			defer wg.Done()
			<-ready
			s.Unsubscribe()
		}(sub)
		go func() {
			defer wg.Done()
			<-ready
			if _, err := d.Publish(context.Background(), "u/1", "p", nil, nil); err != nil {
				t.Errorf("publish during churn: %v", err)
			}
		}()
	}
	close(ready)
	wg.Wait()

	if ids := d.Targets("u/1", "p"); len(ids) != 0 {
		t.Fatalf("targets after mass unsubscribe = %v", ids)
	}
}

func TestContextCancelDoesNotConsume(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 10, DrainOnUnsubscribe: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sub.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := d.Publish(context.Background(), "u/1", "p", 7, nil); err != nil {
		t.Fatal(err)
	}
	sub.Unsubscribe()
	ev, err := sub.Next(context.Background())
	if err != nil || ev.Value != 7 {
		t.Fatalf("after ctx cancel read = (%v,%v), want value 7", ev.Value, err)
	}
}
