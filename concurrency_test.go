package ontology

import (
	"sync"
	"testing"
)

// A subscriber that never reads must not block Publish nor starve
// other subscribers. If Publish could block, this test would hang and
// be killed by the go test timeout.
func TestSlowSubscriberDoesNotBlockProducer(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	const total = 2000
	mustSubscribe(t, d, SubscribeOptions{
		ID: "slow", Prefix: "e", BufferSize: 1, Full: FullDropNewest,
	})
	fast := mustSubscribe(t, d, SubscribeOptions{
		ID: "fast", Prefix: "e", BufferSize: total,
	})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < total/4; i++ {
				if err := d.Publish("e1", "p", i); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got := tryRecvSeqs(fast)
	if len(got) != total {
		t.Fatalf("fast subscriber got %d of %d", len(got), total)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("fast subscriber seqs not increasing at %d", i)
		}
	}
}

// Mixed stress: concurrent publishers, readers, cancels, and a final
// Close. Run with -race to catch data races.
func TestConcurrentStress(t *testing.T) {
	d := NewDispatcher()
	subs := make([]*Subscription, 8)
	for i := range subs {
		subs[i] = mustSubscribe(t, d, SubscribeOptions{
			Prefix: "e", BufferSize: 16,
			Full: FullPolicy(i % 3), Drain: DrainPolicy(i % 2),
		})
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if err := d.Publish("e1", "p", i); err == ErrClosed {
					return
				}
			}
		}()
	}
	for _, s := range subs {
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			for {
				if _, ok := s.Receive(); !ok {
					return
				}
			}
		}(s)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			subs[i].Cancel()
		}(i)
	}
	d.Close()
	wg.Wait()
	d.Close()
}
