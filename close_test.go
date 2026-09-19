package ontology

import (
	"sync"
	"testing"
)

func TestCloseRejectsPublishAndSubscribe(t *testing.T) {
	d := New()
	s, _ := d.Subscribe("e", 5)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != ErrClosed {
		t.Fatalf("publish after close err=%v, want ErrClosed", err)
	}
	if _, err := d.Subscribe("e", 5); err != ErrClosed {
		t.Fatalf("subscribe after close err=%v, want ErrClosed", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second close not idempotent: %v", err)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("subscriber channel not closed on shutdown")
	}
}

func TestCloseTailPolicies(t *testing.T) {
	d := New()
	drain, _ := d.Subscribe("e", 5, WithTail(DrainPending))
	drop, _ := d.Subscribe("e", 5, WithTail(DropPending))
	publishN(t, d, 3)
	d.Close()

	if got := drainAll(drain.C()); len(got) != 3 {
		t.Fatalf("DrainPending readable after close: got %d, want 3", len(got))
	}
	if got := drainAll(drop.C()); len(got) != 0 {
		t.Fatalf("DropPending readable after close: got %d, want 0", len(got))
	}
	if st := drop.Stats(); st.Dropped != 3 {
		t.Fatalf("drop stats %+v, want dropped=3", st)
	}
}

func drainAll(ch <-chan Event) []uint64 {
	var seqs []uint64
	for ev := range ch {
		seqs = append(seqs, ev.Seq)
	}
	return seqs
}

// Every Publish that reports success must have reached every matching
// subscriber; a subscriber's channel closing never coincides with a partial
// fan-out. Verified by hammering Publish against Close concurrently.
func TestCloseAtomicity(t *testing.T) {
	d := New()
	const n = 300
	subs := make([]*Subscription, 4)
	for i := range subs {
		// Buffers never overflow (max n publishes); DrainPending keeps every
		// delivered event readable past Close.
		subs[i], _ = d.Subscribe("e", n+1, WithTail(DrainPending))
	}

	var accepted int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, err := d.Publish(Change{Entity: "e", Attribute: "a"})
			if err == ErrClosed {
				return
			}
			if err != nil {
				t.Errorf("unexpected publish error: %v", err)
				return
			}
			accepted++
		}
	}()
	d.Close()
	wg.Wait()

	for i, s := range subs {
		got := drainAll(s.C())
		if len(got) != accepted {
			t.Fatalf("subscriber %d got %d events, accepted publishes=%d (partial fan-out?)",
				i, len(got), accepted)
		}
		for j, seq := range got {
			if seq != uint64(j+1) {
				t.Fatalf("subscriber %d gap at %d: got seq %d", i, j, seq)
			}
		}
	}
}
