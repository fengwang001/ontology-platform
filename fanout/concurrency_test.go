
package fanout

import (
	"errors"
	"sync"
	"testing"
)

// A completely stalled subscriber (never drains) must never block
// publishers or the other subscriber. Runs under -race.
func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	d := New()
	defer d.Close()

	stalled, err := d.Subscribe("", nil, Options{Buffer: 1, OnFull: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := d.Subscribe("", nil, Options{Buffer: 1, OnFull: DropOldest})
	if err != nil {
		t.Fatal(err)
	}

	const n = 2000
	consumed := make(chan uint64, n)
	consumeDone := make(chan struct{})
	go func() {
		defer close(consumeDone)
		for i := 0; i < n; i++ {
			m := <-fast.C()
			consumed <- m.Seq
		}
	}()

	// Synchronous publishes: under the correct implementation these return
	// immediately even though "stalled" is permanently full.
	for i := 0; i < n; i++ {
		if _, err := d.Publish(Change{Entity: "e", Attr: "a"}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	<-consumeDone

	st := stalled.Stats()
	if st.Dropped != n-1 {
		t.Fatalf("stalled drops = %d want %d", st.Dropped, n-1)
	}

	// The fast subscriber must have seen strictly increasing seq numbers.
	close(consumed)
	var prev uint64
	count := 0
	for seq := range consumed {
		if seq <= prev {
			t.Fatalf("non-increasing seq: %d after %d", seq, prev)
		}
		prev = seq
		count++
	}
	if count != n {
		t.Fatalf("fast got %d want %d", count, n)
	}
}

// Concurrent publish/close: every Publish that reports success must have
// fanned out to every matching subscriber (no partial fan-out).
func TestCloseAtomicUnderConcurrency(t *testing.T) {
	d := New()
	const subsN, pubs = 4, 3000

	type sub struct {
		s    *Subscription
		got  map[uint64]bool
	}
	subs := make([]*sub, subsN)
	for i := range subs {
		s, _ := d.Subscribe("", nil, Options{Buffer: pubs, OnCancel: CancelDrain})
		subs[i] = &sub{s: s, got: make(map[uint64]bool)}
	}

	var readers sync.WaitGroup
	for _, sb := range subs {
		sb := sb
		readers.Add(1)
		go func() {
			defer readers.Done()
			for m := range sb.s.C() {
				sb.got[m.Seq] = true
			}
		}(sb)
	}

	var wg sync.WaitGroup
	var pubMu sync.Mutex
	success := make(map[uint64]bool)
	for i := 0; i < pubs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := d.Publish(Change{Entity: "e"})
			switch {
			case err == nil:
				pubMu.Lock()
				success[seq] = true
				pubMu.Unlock()
			case errors.Is(err, ErrDispatcherClosed):
			default:
				t.Errorf("unexpected publish error: %v", err)
			}
		}()
	}
	// Gate closed through a synchronization point: close concurrently too.
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.Close()
	}()
	wg.Wait()

	// Drain-mode close guarantees every enqueued envelope is forwarded.
	readers.Wait()

	for seq := range success {
		for i, sb := range subs {
			if !sb.got[seq] {
				t.Fatalf("successful publish seq %d missing at subscriber %d", seq, i)
			}
		}
}

func TestConcurrentUnsubscribe(t *testing.T) {
	d := New()
	const n = 100
	created := make([]*Subscription, n)
	for i := range created {
		s, _ := d.Subscribe("", nil, Options{Buffer: 8, OnCancel: CancelPurge})
		created[i] = s
	}

	var wg sync.WaitGroup
	for _, s := range created {
		wg.Add(2)
		go func(s *Subscription) { defer wg.Done(); d.Unsubscribe(s) }(s)
		go func(s *Subscription) { defer wg.Done(); d.Unsubscribe(s) }(s)
	}
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = d.Publish(Change{Entity: "e"}) }()
	}
	wg.Wait()
	d.Close()
}
