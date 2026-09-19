package ontology

import (
	"sync"
	"testing"
)

func TestUnsubscribeStopsDelivery(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 8, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Publish("e", "p", 1); err != nil {
		t.Fatal(err)
	}
	s.Unsubscribe()
	if err := d.Publish("e", "p", 2); err != nil {
		t.Fatal(err)
	}
	got := recvAll(t, s)
	if !equalSeqs(got, []uint64{1}) {
		t.Fatalf("received %v after unsubscribe, want [1]", got)
	}
	if ids := d.Match("e", "p"); len(ids) != 0 {
		t.Fatalf("unsubscribed subscription still matched: %v", ids)
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.Unsubscribe()
	s.Unsubscribe()
	s.Unsubscribe()
	// Unsubscribing again after the dispatcher is closed is also safe.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	s.Unsubscribe()
}

func TestConcurrentUnsubscribe(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Unsubscribe()
		}()
	}
	wg.Wait()
	if ids := d.Match("e", "p"); len(ids) != 0 {
		t.Fatalf("subscription survived concurrent unsubscribe: %v", ids)
	}
}

func TestUnsubscribeDuringFanoutKeepsOthers(t *testing.T) {
	d := New()
	defer d.Close()
	const n = 200
	victim, err := d.Subscribe(Options{Buffer: n, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	bystander, err := d.Subscribe(Options{Buffer: n, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := d.Publish("e", "p", i); err != nil {
				t.Errorf("Publish failed during concurrent unsubscribe: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		victim.Unsubscribe()
	}()
	wg.Wait()
	bystander.Unsubscribe()
	got := recvAll(t, bystander)
	if len(got) != n {
		t.Fatalf("bystander received %d messages, want all %d", len(got), n)
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("bystander seqs = %v..., want 1..%d contiguous", got[:5], n)
		}
	}
}

func TestUnsubscribeDropsQueuedWhenNoDrain(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 4}) // DrainOnClose = false
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	s.Unsubscribe()
	if got := recvAll(t, s); len(got) != 0 {
		t.Fatalf("received %v, want queued messages discarded", got)
	}
	if s.Dropped() != 3 {
		t.Fatalf("Dropped() = %d, want 3 queued messages counted", s.Dropped())
	}
	if s.LastDropSeq() != 3 {
		t.Fatalf("LastDropSeq() = %d, want 3", s.LastDropSeq())
	}
}
