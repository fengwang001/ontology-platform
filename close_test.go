package ontology

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestCloseRejectsPublishAndSubscribe(t *testing.T) {
	d := New()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("repeated close = %v, want nil", err)
	}
	if _, err := d.Publish(context.Background(), "u", "p", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close = %v, want ErrClosed", err)
	}
	if _, err := d.Subscribe("u", nil, Options{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close = %v, want ErrClosed", err)
	}
}

func TestCloseFinalizesQueuesByDeclaration(t *testing.T) {
	d := New()
	drainSub, _ := d.Subscribe("u", []string{"a"}, Options{Capacity: 10, DrainOnUnsubscribe: true})
	dropSub, _ := d.Subscribe("u", []string{"b"}, Options{Capacity: 10})

	ctx := context.Background()
	d.Publish(ctx, "u/1", "a", 1, nil)
	d.Publish(ctx, "u/1", "a", 2, nil)
	d.Publish(ctx, "u/1", "b", 3, nil)

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := drainSub.Next(ctx)
	if err != nil || got.Value != 1 {
		t.Fatalf("drain first = (%v,%v), want 1", got.Value, err)
	}
	got, err = drainSub.Next(ctx)
	if err != nil || got.Value != 2 {
		t.Fatalf("drain second = (%v,%v), want 2", got.Value, err)
	}
	if _, err := drainSub.Next(ctx); !errors.Is(err, ErrDrained) {
		t.Fatalf("after drain = %v, want ErrDrained", err)
	}
	if _, err := dropSub.Next(ctx); !errors.Is(err, ErrSubscriptionGone) {
		t.Fatalf("non-draining queue = %v, want ErrSubscriptionGone", err)
	}
}

// Close racing with many Publish calls must leave the system in one of two
// states: a publish fully completed (all matching, non-overflowing
// subscribers received it) or it was rejected with ErrClosed.
func TestCloseIsAtomicWithPublish(t *testing.T) {
	d := New()
	const subscribers = 16
	var subs []*Subscription
	got := make([]int64, subscribers)
	ready := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < subscribers; i++ {
		sub, _ := d.Subscribe("u", nil, Options{Capacity: 1, Policy: DropNewest})
		subs = append(subs, sub)
	}

	// Consumers record the single seq they hold (capacity 1), coordinating
	// purely through channels.
	consumerDone := make(chan struct{}, subscribers)
	for i, sub := range subs {
		go func(i int, s *Subscription) {
			defer func() { consumerDone <- struct{}{} }()
			<-ready
			ev, err := s.Next(context.Background())
			if err == nil {
				got[i] = ev.Seq
			}
		}(i, sub)
	}

	pubErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-ready
		_, err := d.Publish(context.Background(), "u/1", "p", nil, nil)
		pubErr <- err
	}()
	go func() {
		defer wg.Done()
		<-ready
		d.Close()
	}()
	close(ready)
	wg.Wait()

	err := <-pubErr
	if err != nil && !errors.Is(err, ErrClosed) {
		t.Fatalf("publish error = %v, want nil or ErrClosed", err)
	}
	if err == nil {
		// A completed publish must have reached every subscriber.
		for i := 0; i < subscribers; i++ {
			<-consumerDone
			if got[i] != 1 {
				t.Fatalf("subscriber %d got seq %d, want full delivery seq 1", i, got[i])
			}
		}
	} else {
		for i := 0; i < subscribers; i++ {
			<-consumerDone
		}
	}
}
