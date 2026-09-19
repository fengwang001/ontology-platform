package ontology

import (
	"sync"
	"testing"
)

func TestUnsubscribeStopsDelivery(t *testing.T) {
	d := New()
	s, _ := d.Subscribe("e", 5)
	d.Publish(Change{Entity: "e", Attribute: "a"})
	d.Unsubscribe(s.ID())
	if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
		t.Fatalf("publish after unsubscribe failed: %v", err)
	}
	if ids := d.Targets("e", "a"); len(ids) != 0 {
		t.Fatalf("removed subscriber still a target: %v", ids)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("channel not closed after unsubscribe")
	}
	if st := s.Stats(); !st.Closed {
		t.Fatalf("stats after unsubscribe: %+v", st)
	}
}

func TestUnsubscribeIdempotent(t *testing.T) {
	d := New()
	s, _ := d.Subscribe("e", 5)
	d.Unsubscribe(s.ID())
	d.Unsubscribe(s.ID())
	d.Unsubscribe(s.ID() + 999)
	d.Unsubscribe(0)
}

// DrainPending: already buffered events remain readable after cancel.
func TestUnsubscribeDrainPending(t *testing.T) {
	d := New()
	s, _ := d.Subscribe("e", 5, WithTail(DrainPending))
	publishN(t, d, 3)
	d.Unsubscribe(s.ID())
	if got := drain(s.C()); len(got) != 3 {
		t.Fatalf("drained %d events, want 3", len(got))
	}
	if st := s.Stats(); st.Dropped != 0 {
		t.Fatalf("drain tail dropped %d, want 0", st.Dropped)
	}
}

// DropPending: buffered events are discarded (and counted) on cancel.
func TestUnsubscribeDropPending(t *testing.T) {
	d := New()
	s, _ := d.Subscribe("e", 5, WithTail(DropPending))
	publishN(t, d, 3)
	d.Unsubscribe(s.ID())
	if got := drain(s.C()); len(got) != 0 {
		t.Fatalf("read %d events after DropPending, want 0", len(got))
	}
	if st := s.Stats(); st.Dropped != 3 || st.LastDropSeq != 3 {
		t.Fatalf("stats = %+v, want dropped=3 lastDrop=3", st)
	}
}

// Concurrently unsubscribing one subscriber must never make Publish fail or
// lose events destined for the other subscriber.
func TestUnsubscribeConcurrent(t *testing.T) {
	d := New()
	victim, _ := d.Subscribe("e", 1, WithOverflow(DropNewest))
	survivor, _ := d.Subscribe("e", 10000)

	const publishes = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.Unsubscribe(victim.ID())
		d.Unsubscribe(victim.ID())
	}()
	for i := 0; i < publishes; i++ {
		if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}
	wg.Wait()
	d.Close()

	got := drain(survivor.C())
	if len(got) != publishes {
		t.Fatalf("survivor got %d, want all %d", len(got), publishes)
	}
	for i, ev := range got {
		if ev != uint64(i+1) {
			t.Fatalf("survivor sequence gap at %d: %d", i, ev)
		}
	}
}
