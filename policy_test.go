package ontology

import "testing"

// recvAll drains every currently and subsequently queued message until
// the channel closes, returning the received sequence numbers.
func recvAll(t *testing.T, s *Subscription) []uint64 {
	t.Helper()
	var seqs []uint64
	for m := range s.C() {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDropOldestEvictsAndCounts(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 2, OnFull: DropOldest, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	s.Unsubscribe()
	got := recvAll(t, s)
	if !equalSeqs(got, []uint64{3, 4}) {
		t.Fatalf("DropOldest received %v, want [3 4]", got)
	}
	if s.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", s.Dropped())
	}
	if s.LastDropSeq() != 2 {
		t.Fatalf("LastDropSeq() = %d, want 2 (evicted oldest)", s.LastDropSeq())
	}
}

func TestDropNewestDiscardsIncoming(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 2, OnFull: DropNewest, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	s.Unsubscribe()
	got := recvAll(t, s)
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("DropNewest received %v, want [1 2]", got)
	}
	if s.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", s.Dropped())
	}
	if s.LastDropSeq() != 4 {
		t.Fatalf("LastDropSeq() = %d, want 4 (discarded incoming)", s.LastDropSeq())
	}
}

func TestDisconnectMarksLaggingAndStops(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 1, OnFull: Disconnect})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Lagged() {
		t.Fatal("Lagged() = false, want true after overflow")
	}
	// Seq 1 was queued, seq 2 overflowed and triggered the disconnect;
	// seqs 3 and 4 never reached the subscription. Without
	// DrainOnClose the queued message is discarded and counted.
	if got := recvAll(t, s); len(got) != 0 {
		t.Fatalf("received %v after disconnect, want nothing", got)
	}
	if s.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2 (incoming + queued)", s.Dropped())
	}
	if s.LastDropSeq() != 2 {
		t.Fatalf("LastDropSeq() = %d, want 2", s.LastDropSeq())
	}
	if ids := d.Match("e", "p"); len(ids) != 0 {
		t.Fatalf("disconnected subscription still matched: %v", ids)
	}
}

func TestDisconnectWithDrainKeepsQueued(t *testing.T) {
	d := New()
	defer d.Close()
	s, err := d.Subscribe(Options{Buffer: 2, OnFull: Disconnect, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	got := recvAll(t, s)
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("drained %v, want [1 2]", got)
	}
	if s.Dropped() != 1 || s.LastDropSeq() != 3 {
		t.Fatalf("Dropped=%d LastDropSeq=%d, want 1 and 3", s.Dropped(), s.LastDropSeq())
	}
}

func TestPoliciesAreIndependentPerSubscriber(t *testing.T) {
	d := New()
	defer d.Close()
	dropper, err := d.Subscribe(Options{Buffer: 1, OnFull: DropNewest, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	full, err := d.Subscribe(Options{Buffer: 16, OnFull: DropNewest, DrainOnClose: true})
	if err != nil {
		t.Fatal(err)
	}
	const n = 5
	for i := 0; i < n; i++ {
		if err := d.Publish("e", "p", i); err != nil {
			t.Fatal(err)
		}
	}
	dropper.Unsubscribe()
	full.Unsubscribe()
	if got := recvAll(t, dropper); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("dropper received %v, want [1]", got)
	}
	if dropper.Dropped() != n-1 {
		t.Fatalf("dropper Dropped() = %d, want %d", dropper.Dropped(), n-1)
	}
	got := recvAll(t, full)
	want := []uint64{1, 2, 3, 4, 5}
	if !equalSeqs(got, want) {
		t.Fatalf("full subscriber received %v, want %v", got, want)
	}
	if full.Dropped() != 0 {
		t.Fatalf("full subscriber Dropped() = %d, want 0", full.Dropped())
	}
}
