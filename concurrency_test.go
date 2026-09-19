package ontology

import (
	"sync"
	"testing"
)

func TestSlowSubscriberDoesNotBlockProducer(t *testing.T) {
	d := New()
	defer d.Close()
	// The slow subscriber never reads; its queue fills immediately.
	slow, err := d.Subscribe(Options{Buffer: 1, OnFull: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Unsubscribe()
	const n = 500
	fast, err := d.Subscribe(Options{Buffer: n, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan uint64, n)
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for m := range fast.C() {
			received <- m.Seq
		}
	}()
	// If Publish ever blocked on the slow subscriber, this loop would
	// deadlock and the test would time out.
	var producer sync.WaitGroup
	producer.Add(1)
	go func() {
		defer producer.Done()
		for i := 0; i < n; i++ {
			if err := d.Publish("e", "p", i); err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
		}
	}()
	producer.Wait()
	fast.Unsubscribe()
	reader.Wait()
	close(received)
	count := 0
	var prev uint64
	for seq := range received {
		count++
		if count > 1 && seq <= prev {
			t.Fatalf("seqs not strictly increasing: %d after %d", seq, prev)
		}
		prev = seq
	}
	if count != n {
		t.Fatalf("fast subscriber received %d of %d messages", count, n)
	}
	if slow.Dropped() == 0 {
		t.Fatal("slow subscriber should have dropped messages")
	}
}

func TestConcurrentPublishersGetUniqueMonotonicSeqs(t *testing.T) {
	d := New()
	defer d.Close()
	const publishers = 8
	const each = 100
	s, err := d.Subscribe(Options{Buffer: publishers * each, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := d.Publish("e", "p", p*each+i); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}(p)
	}
	wg.Wait()
	s.Unsubscribe()
	got := recvAll(t, s)
	if len(got) != publishers*each {
		t.Fatalf("received %d messages, want %d", len(got), publishers*each)
	}
	seen := make(map[uint64]bool, len(got))
	for i, seq := range got {
		if i > 0 && seq <= got[i-1] {
			t.Fatalf("seqs not strictly increasing at %d: %v", i, got[i-1:i+1])
		}
		if seen[seq] {
			t.Fatalf("duplicate seq %d", seq)
		}
		seen[seq] = true
	}
	if s.Dropped() != 0 {
		t.Fatalf("Dropped() = %d, want 0 with ample buffer", s.Dropped())
	}
}

func TestConcurrentSubscribePublishUnsubscribe(t *testing.T) {
	d := New()
	defer d.Close()
	anchor, err := d.Subscribe(Options{Buffer: 256, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s, err := d.Subscribe(Options{Buffer: 4, OnFull: Disconnect})
				if err != nil {
					return
				}
				s.Unsubscribe()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			if err := d.Publish("e", "p", j); err != nil {
				return
			}
		}
	}()
	wg.Wait()
	anchor.Unsubscribe()
	got := recvAll(t, anchor)
	if len(got) != 200 {
		t.Fatalf("anchor received %d of 200 messages", len(got))
	}
}
