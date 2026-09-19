package ontology

import (
	"sync"
	"testing"
	"time"
)

// A subscriber whose buffer is full and never drained must not make Publish
// block: all publishes complete promptly and another subscriber gets
// everything.
func TestProducerNotBlockedBySlowSubscriber(t *testing.T) {
	d := New()
	slow, _ := d.Subscribe("e", 1, WithOverflow(DropNewest))
	fast, _ := d.Subscribe("e", 10000, WithOverflow(DropNewest))
	// Fill the slow buffer; nobody will ever drain it.
	publishN(t, d, 2)

	const n = 1000
	done := make(chan struct{})
	go func() {
		publishN(t, d, n)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked behind a full slow-subscriber queue")
	}
	d.Close()

	got := drain(fast.C())
	if len(got) != n+2 {
		t.Fatalf("fast subscriber got %d, want %d", len(got), n+2)
	}
	if st := slow.Stats(); st.Dropped == 0 {
		t.Fatal("slow subscriber recorded no drops despite overflow")
	}
}

// Concurrent publish / subscribe / unsubscribe / query / stats churn must
// stay race-free and never panic; only one Close wins, always safely.
func TestConcurrentChurn(t *testing.T) {
	d := New()
	var wg sync.WaitGroup

	for p := 0; p < 3; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if seq, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err == nil && seq == 0 {
					t.Error("zero seq without error")
					return
				}
			}
		}()
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			policy := []OverflowPolicy{DropOldest, DropNewest, LagDisconnect}[i%3]
			s, err := d.Subscribe("e", 2, WithOverflow(policy))
			if err != nil {
				return // dispatcher closed
			}
			stop := make(chan struct{})
			go func() {
				for {
					select {
					case <-s.C():
					case <-stop:
						return
					}
				}
			}()
			for j := 0; j < 200; j++ {
				s.Stats()
				d.Targets("e", "a")
			}
			close(stop)
			d.Unsubscribe(s.ID())
			d.Unsubscribe(s.ID())
		}(i)
	}

	wg.Wait()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
}

// Fan-out independence under concurrency: two overflowing subscribers each
// keep internally consistent (received + dropped == published) while a third
// subscriber receives every event.
func TestConcurrentOverflowIndependence(t *testing.T) {
	d := New()
	a, _ := d.Subscribe("e", 2, WithOverflow(DropOldest))
	b, _ := d.Subscribe("e", 2, WithOverflow(DropNewest))
	c, _ := d.Subscribe("e", 5000)

	const n = 1000
	publishN(t, d, n)

	var wg sync.WaitGroup
	consume := func(s *Subscription) {
		defer wg.Done()
		var count int
		var prev uint64
		for ev := range s.C() {
			if ev.Seq <= prev {
				t.Errorf("non-increasing seq: %d after %d", ev.Seq, prev)
			}
			prev = ev.Seq
			count++
		}
		if uint64(count)+s.Stats().Dropped != n {
			t.Errorf("subscriber accounting: received %d + dropped %d != %d",
				count, s.Stats().Dropped, n)
		}
	}
	wg.Add(2)
	go consume(a)
	go consume(b)
	d.Close()
	wg.Wait()

	if got := drain(c.C()); len(got) != n {
		t.Fatalf("full subscriber got %d, want %d", len(got), n)
	}
}
