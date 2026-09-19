package ontology

import "testing"

func drain(ch <-chan Event) []uint64 {
	var seqs []uint64
	for ev := range ch {
		seqs = append(seqs, ev.Seq)
	}
	return seqs
}

func publishN(t *testing.T, d *Dispatcher, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := d.Publish(Change{Entity: "e", Attribute: "a"}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
}

// DropOldest: a buffer of 2 fed 4 events retains only the newest two; the
// two oldest events are counted as drops.
func TestOverflowDropOldest(t *testing.T) {
	d := New()
	s, err := d.Subscribe("e", 2, WithOverflow(DropOldest))
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 4)

	d.Close()
	if got := drain(s.C()); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("retained %v, want [3 4]", got)
	}
	st := s.Stats()
	if st.Dropped != 2 || st.LastDropSeq != 2 {
		t.Fatalf("stats = %+v, want dropped=2 lastDrop=2", st)
	}
}

// DropNewest: the original two events stay queued; every later event is
// dropped and the last dropped sequence is the final publish.
func TestOverflowDropNewest(t *testing.T) {
	d := New()
	s, err := d.Subscribe("e", 2, WithOverflow(DropNewest))
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 4)

	d.Close()
	if got := drain(s.C()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("retained %v, want [1 2]", got)
	}
	st := s.Stats()
	if st.Dropped != 2 || st.LastDropSeq != 4 {
		t.Fatalf("stats = %+v, want dropped=2 lastDrop=4", st)
	}
}

// LagDisconnect: on overflow the new event is counted, the subscriber is
// marked lagging and its channel is closed.
func TestOverflowLagDisconnect(t *testing.T) {
	d := New()
	s, err := d.Subscribe("e", 2, WithOverflow(LagDisconnect))
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 4)

	if got := drain(s.C()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("retained %v, want [1 2]", got)
	}
	st := s.Stats()
	if !st.Lagging || !st.Closed || st.Dropped != 1 || st.LastDropSeq != 3 {
		t.Fatalf("stats = %+v, want lagging closed dropped=1 lastDrop=3", st)
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("channel not closed after lag disconnect")
	}
	// Further publishes never reach or panic on the disconnected subscriber.
	publishN(t, d, 2)
	if st := s.Stats(); st.Dropped != 1 {
		t.Fatalf("dropped changed after disconnect: %+v", st)
	}
}

// One subscriber overflowing must not affect another subscriber at all.
func TestOverflowIsolation(t *testing.T) {
	d := New()
	full, err := d.Subscribe("e", 1, WithOverflow(DropNewest))
	if err != nil {
		t.Fatal(err)
	}
	empty, err := d.Subscribe("e", 10, WithOverflow(DropNewest))
	if err != nil {
		t.Fatal(err)
	}
	publishN(t, d, 6)

	d.Close()
	if got := drain(full.C()); len(got) != 1 || got[0] != 1 {
		t.Fatalf("slow subscriber got %v, want [1]", got)
	}
	if got := drain(empty.C()); len(got) != 6 {
		t.Fatalf("fast subscriber got %d events, want all 6", len(got))
	}
	if st := empty.Stats(); st.Dropped != 0 {
		t.Fatalf("fast subscriber dropped %d, want 0", st.Dropped)
	}
}
