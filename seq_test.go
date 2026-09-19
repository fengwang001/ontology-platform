package ontology

import "testing"

// Received sequences plus dropped sequences must partition the published
// sequence space: every sequence 1..N is accounted for exactly once, and
// delivered sequences are strictly increasing.
func TestSequenceGapAccounting(t *testing.T) {
	for _, p := range []OverflowPolicy{DropOldest, DropNewest} {
		d := New()
		s, err := d.Subscribe("e", 2, WithOverflow(p))
		if err != nil {
			t.Fatal(err)
		}
		const n = 10
		publishN(t, d, n)
		d.Close()
		received := drain(s.C())

		seen := make(map[uint64]bool)
		var prev uint64
		for i, seq := range received {
			if i > 0 && seq <= prev {
				t.Fatalf("policy %d: sequences not strictly increasing: %v", p, received)
			}
			prev = seq
			seen[seq] = true
		}
		st := s.Stats()
		if uint64(len(received))+st.Dropped != n {
			t.Fatalf("policy %d: received %d + dropped %d != %d",
				p, len(received), st.Dropped, n)
		}
		// Reconstruct the missing intervals from the observed gaps.
		missing := uint64(0)
		want := uint64(1)
		for _, seq := range received {
			missing += seq - want
			want = seq + 1
		}
		missing += n + 1 - want
		if missing != st.Dropped {
			t.Fatalf("policy %d: gap-derived drops %d != counter %d",
				p, missing, st.Dropped)
		}
	}
}

// Publish returns globally increasing, contiguous sequence numbers.
func TestPublishSequenceMonotonic(t *testing.T) {
	d := New()
	var prev uint64
	for i := 1; i <= 50; i++ {
		seq, err := d.Publish(Change{Entity: "e", Attribute: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if seq != uint64(i) || seq <= prev {
			t.Fatalf("seq=%d prev=%d at i=%d", seq, prev, i)
		}
		prev = seq
	}
}

// Two independent subscribers see the same global sequence numbering.
func TestSequenceSharedAcrossSubscribers(t *testing.T) {
	d := New()
	a, _ := d.Subscribe("e", 1)
	b, _ := d.Subscribe("e", 1, WithOverflow(DropNewest))

	seq1, _ := d.Publish(Change{Entity: "e", Attribute: "a"})
	ev1 := <-a.C()
	ev2 := <-b.C()
	if ev1.Seq != seq1 || ev2.Seq != seq1 {
		t.Fatalf("seqs mismatch: publish=%d a=%d b=%d", seq1, ev1.Seq, ev2.Seq)
	}
}
