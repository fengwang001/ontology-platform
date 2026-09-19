
package fanout

import (
	"errors"
	"testing"
)

func publishN(t *testing.T, d *Dispatcher, n int) []uint64 {
	t.Helper()
	seqs := make([]uint64, n)
	for i := 0; i < n; i++ {
		seq, err := d.Publish(Change{Entity: "user", Attr: "name"})
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		seqs[i] = seq
	}
	return seqs
}

func drainSeqs(s *Subscription, n int) []uint64 {
	got := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		m, ok := <-s.C()
		if !ok {
			return got
		}
		got = append(got, m.Seq)
	}
	return got
}

func TestDropNewestKeepsOldest(t *testing.T) {
	d := New()
	s, err := d.Subscribe("", nil, Options{Buffer: 2, OnFull: DropNewest})
	if err != nil {
		t.Fatal(err)
	}
	seqs := publishN(t, d, 5)

	got := drainSeqs(s, 2)
	if !equal(got, seqs[:2]) {
		t.Fatalf("got %v want first two %v", got, seqs[:2])
	}

	st := s.Stats()
	if st.Dropped != 3 || st.LastDroppedSeq != seqs[4] {
		t.Fatalf("stats = %+v, want 3 drops ending at %d", st, seqs[4])
	}

	// Gap accounting: delivered 1,2 and last dropped 5 -> missed 3,4,5.
	missed := gaps(got, st.LastDroppedSeq)
	if !equal(missed, []uint64{3, 4, 5}) {
		t.Fatalf("missed gaps = %v", missed)
	}
}

func TestDropOldestKeepsNewest(t *testing.T) {
	d := New()
	s, err := d.Subscribe("", nil, Options{Buffer: 2, OnFull: DropOldest})
	if err != nil {
		t.Fatal(err)
	}
	seqs := publishN(t, d, 5)

	got := drainSeqs(s, 2)
	if !equal(got, seqs[3:]) {
		t.Fatalf("got %v want newest two %v", got, seqs[3:])
	}

	st := s.Stats()
	if st.Dropped != 3 || st.LastDroppedSeq != seqs[2] {
		t.Fatalf("stats = %+v, want 3 drops, last evicted %d", st, seqs[2])
	}
}

func TestDropDisconnectDetaches(t *testing.T) {
	d := New()
	s, err := d.Subscribe("", nil, Options{
		Buffer: 1, OnFull: DropDisconnect, OnCancel: CancelPurge,
	})
	if err != nil {
		t.Fatal(err)
	}
	seqs := publishN(t, d, 4)

	got := drainSeqs(s, 1)
	if !equal(got, seqs[:1]) {
		t.Fatalf("got %v want %v", got, seqs[:1])
	}
	if _, ok := <-s.C(); ok {
		t.Fatal("channel should be closed after detach")
	}
	st := s.Stats()
	if !st.Detached {
		t.Fatal("subscriber should be detached")
	}
	// The triggering message 2 and later messages 3,4 are all missed.
	if st.Dropped != 3 || st.LastDroppedSeq != 4 {
		t.Fatalf("stats = %+v", st)
	}

	// A closed-dispatcher Publish after detach is still rejected globally.
	d.Close()
	if _, err := d.Publish(Change{Entity: "x"}); !errors.Is(err, ErrDispatcherClosed) {
		t.Fatalf("want closed error, got %v", err)
	}
}

func TestOneDropperDoesNotAffectAnother(t *testing.T) {
	d := New()
	slow, _ := d.Subscribe("", nil, Options{Buffer: 1, OnFull: DropNewest})
	fast, _ := d.Subscribe("", nil, Options{Buffer: 10, OnFull: DropNewest})

	seqs := publishN(t, d, 6)

	fastGot := drainSeqs(fast, 6)
	if !equal(fastGot, seqs) {
		t.Fatalf("fast subscriber lost messages: %v", fastGot)
	}
	if st := fast.Stats(); st.Dropped != 0 {
		t.Fatalf("fast subscriber dropped %d", st.Dropped)
	}
	slowGot := drainSeqs(slow, 1)
	if !equal(slowGot, seqs[:1]) {
		t.Fatalf("slow got %v", slowGot)
	}
	if st := slow.Stats(); st.Dropped != 5 {
		t.Fatalf("slow drops = %d, want 5", st.Dropped)
	}
}

func equal(a, b []uint64) bool {
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

// gaps reconstructs missed sequence numbers between received messages,
// i.e. after the last received seq up to and including lastDropSeq.
func gaps(received []uint64, lastDropSeq uint64) []uint64 {
	if len(received) == 0 || lastDropSeq <= received[len(received)-1] {
		return nil
	}
	var out []uint64
	for x := received[len(received)-1] + 1; x <= lastDropSeq; x++ {
		out = append(out, x)
	}
	return out
}
