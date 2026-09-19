package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestCloseRejectsPublishAndSubscribe(t *testing.T) {
	d := New()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Publish("e", "p", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after Close = %v, want ErrClosed", err)
	}
	if _, err := d.Subscribe(Options{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after Close = %v, want ErrClosed", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	d := New()
	for i := 0; i < 3; i++ {
		if err := d.Close(); err != nil {
			t.Fatalf("Close #%d = %v, want nil", i, err)
		}
	}
}

func TestCloseDrainsPerPolicy(t *testing.T) {
	d := New()
	drained, err := d.Subscribe(Options{Buffer: 4, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := d.Subscribe(Options{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := recvAll(t, drained); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("drained subscription got %v, want [1 2]", got)
	}
	if got := recvAll(t, dropped); len(got) != 0 {
		t.Fatalf("non-drained subscription got %v, want nothing", got)
	}
	if dropped.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", dropped.Dropped())
	}
}

func TestCloseVsPublishIsAtomic(t *testing.T) {
	// A Publish racing with Close must either complete for every
	// matching subscriber or have no effect at all. After Close
	// returns, both subscribers must hold identical sequences.
	for trial := 0; trial < 50; trial++ {
		d := New()
		a, err := d.Subscribe(Options{Buffer: 1000, DrainOnClose: true})
		if err != nil {
			t.Fatal(err)
		}
		b, err := d.Subscribe(Options{Buffer: 1000, DrainOnClose: true})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := d.Publish("e", "p", i); errors.Is(err, ErrClosed) {
					return
				} else if err != nil {
					t.Errorf("unexpected Publish error: %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			d.Close()
		}()
		wg.Wait()
		ga, gb := recvAll(t, a), recvAll(t, b)
		if !equalSeqs(ga, gb) {
			t.Fatalf("trial %d: partial fan-out across Close: a=%v b=%v", trial, ga, gb)
		}
		for i := 1; i < len(ga); i++ {
			if ga[i] != ga[i-1]+1 {
				t.Fatalf("trial %d: gap in delivered seqs without drops: %v", trial, ga)
			}
		}
	}
}

func TestPublishDeliversToAllMatching(t *testing.T) {
	d := New()
	defer d.Close()
	var subs []*Subscription
	for i := 0; i < 4; i++ {
		s, err := d.Subscribe(Options{Buffer: 4, DrainOnClose: true})
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	if err := d.Publish("e", "p", "v"); err != nil {
		t.Fatal(err)
	}
	for _, s := range subs {
		s.Unsubscribe()
		if got := recvAll(t, s); !equalSeqs(got, []uint64{1}) {
			t.Fatalf("subscription %d got %v, want [1]", s.ID(), got)
		}
	}
}
