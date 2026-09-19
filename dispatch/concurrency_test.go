package dispatch

import (
	"sync"
	"testing"
)

// TestProducerNotBlockedBySlowSubscriber: a subscriber that never reads its
// tiny queue must not slow down producers or starve a healthy subscriber.
// Deterministic: if Publish ever blocked on the slow subscriber, this test
// would deadlock and be killed by the go test timeout.
func TestProducerNotBlockedBySlowSubscriber(t *testing.T) {
	d := New()
	defer d.Close()
	slow := mustSubscribe(t, d, SubscribeOptions{
		ID: "slow", EntityPrefix: "e", BufferSize: 1, Policy: DropNewest,
	})
	const producers = 4
	const perProducer = 250
	const total = producers * perProducer
	fast := mustSubscribe(t, d, SubscribeOptions{
		ID: "fast", EntityPrefix: "e", BufferSize: total, Policy: DropNewest,
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func(p int) {
			defer wg.Done()
			<-start
			for i := 0; i < perProducer; i++ {
				if err := d.Publish("e1", "p", p*perProducer+i); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}(p)
	}
	close(start)
	wg.Wait() // returns only if no producer ever blocked
	got := recvN(t, fast, total)
	if fast.Dropped() != 0 {
		t.Fatalf("fast dropped %d messages, want 0", fast.Dropped())
	}
	seen := make(map[int]bool, total)
	for _, m := range got {
		seen[m.Value.(int)] = true
	}
	if len(seen) != total {
		t.Fatalf("fast received %d distinct values, want %d", len(seen), total)
	}
	if slow.Dropped() != total-1 {
		t.Fatalf("slow dropped %d, want %d", slow.Dropped(), total-1)
	}
}

// TestConcurrentHammer exercises Subscribe, Publish, Cancel and Close
// concurrently to surface data races under -race.
func TestConcurrentHammer(t *testing.T) {
	d := New()
	const subs = 8
	ss := make([]*Subscriber, 0, subs)
	for i := 0; i < subs; i++ {
		s, err := d.Subscribe(SubscribeOptions{
			EntityPrefix: "e", BufferSize: 2,
			Policy: DropOldest, DrainOnCancel: i%2 == 0,
		})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		ss = append(ss, s)
	}
	var wg sync.WaitGroup
	// Readers drain queues until they are closed.
	for _, s := range ss {
		wg.Add(1)
		go func(s *Subscriber) {
			defer wg.Done()
			var prev uint64
			for m := range s.Chan() {
				if m.Seq <= prev {
					t.Errorf("subscriber %q: seq %d after %d", s.ID(), m.Seq, prev)
				}
				prev = m.Seq
			}
		}(s)
	}
	// Publishers.
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = d.Publish("e1", "p", i)
			}
		}()
	}
	// Cancellers.
	for _, s := range ss {
		wg.Add(1)
		go func(s *Subscriber) {
			defer wg.Done()
			s.Cancel()
			s.Cancel()
		}(s)
	}
	wg.Wait()
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Late subscribers must fail.
	if _, err := d.Subscribe(SubscribeOptions{ID: "late"}); err == nil {
		t.Fatal("Subscribe after Close succeeded, want error")
	}
}
