package ontology

import (
	"context"
	"errors"
	"testing"
)

func drainAll(t *testing.T, sub *Subscription) []int64 {
	t.Helper()
	var seqs []int64
	for {
		ev, err := sub.Next(context.Background())
		if err != nil {
			return seqs
		}
		seqs = append(seqs, ev.Seq)
	}
}

// publishN publishes n events to ("u/1", "p") and returns the first/last
// assigned sequence numbers.
func publishN(t *testing.T, d *Dispatcher, n int) (int64, int64) {
	t.Helper()
	var first, last int64
	for i := 0; i < n; i++ {
		seq, err := d.Publish(context.Background(), "u/1", "p", i, nil)
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		if i == 0 {
			first = seq
		}
		last = seq
	}
	return first, last
}

func TestDropOldestKeepsNewestAndAccountsDrops(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 2, Policy: DropOldest, DrainOnUnsubscribe: true})

	first, last := publishN(t, d, 5)
	sub.Unsubscribe()
	seqs := drainAll(t, sub)
	if len(seqs) != 2 || seqs[0] != last-1 || seqs[1] != last {
		t.Fatalf("received %v, want newest two seqs %d,%d", seqs, last-1, last)
	}
	st := sub.Stats()
	if st.Dropped != 3 || st.LastDropSeq != last-2 {
		t.Fatalf("stats = %+v, want Dropped=3 LastDropSeq=%d", st, last-2)
	}
	// Gap accounting: observed sequences + dropped count must cover the
	// entire assigned interval exactly.
	if int64(st.Dropped)+int64(len(seqs)) != last-first+1 {
		t.Fatal("dropped + received does not cover the published interval")
	}
}

func TestDropNewestKeepsOldestAndAccountsDrops(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 2, Policy: DropNewest, DrainOnUnsubscribe: true})

	first, last := publishN(t, d, 5)
	sub.Unsubscribe()
	seqs := drainAll(t, sub)
	if len(seqs) != 2 || seqs[0] != first || seqs[1] != first+1 {
		t.Fatalf("received %v, want oldest two seqs %d,%d", seqs, first, first+1)
	}
	st := sub.Stats()
	if st.Dropped != 3 || st.LastDropSeq != last {
		t.Fatalf("stats = %+v, want Dropped=3 LastDropSeq=%d", st, last)
	}
	// The gap is exactly the three rejected newest sequences.
	if seqs[len(seqs)-1] != first+1 {
		t.Fatal("gap does not start immediately after the buffered events")
	}
}

func TestDisconnectMarksLaggingAndStopsFanout(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 2, Policy: Disconnect})

	first, last := publishN(t, d, 4)
	seqs := drainAll(t, sub)
	if len(seqs) != 2 || seqs[0] != first || seqs[1] != first+1 {
		t.Fatalf("buffered seqs = %v, want %d,%d", seqs, first, first+1)
	}
	_, err := sub.Next(context.Background())
	if !errors.Is(err, ErrDisconnected) {
		t.Fatalf("terminal error = %v, want ErrDisconnected", err)
	}
	st := sub.Stats()
	if !st.Closed || st.Dropped != 1 || st.LastDropSeq != first+2 {
		t.Fatalf("stats = %+v, want closed, Dropped=1 at %d", st, first+2)
	}
	if ids := d.Targets("u/1", "p"); len(ids) != 0 {
		t.Fatalf("disconnected subscriber still targeted: %v", ids)
	}
	// Further publishes do not resurrect it and the terminal error persists.
	if _, err := d.Publish(context.Background(), "u/1", "p", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Next(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("err after more publishes = %v", err)
	}
	_ = last
}

func TestOverflowIsIndependentPerSubscriber(t *testing.T) {
	d := New()
	defer d.Close()
	slow, _ := d.Subscribe("u", nil, Options{Capacity: 1, Policy: DropNewest, DrainOnUnsubscribe: true})
	full, _ := d.Subscribe("u", nil, Options{Capacity: 100, DrainOnUnsubscribe: true})

	first, last := publishN(t, d, 20)
	_ = first
	slow.Unsubscribe()
	full.Unsubscribe()
	slowSeqs := drainAll(t, slow)
	if len(slowSeqs) != 1 || slowSeqs[0] != first {
		t.Fatalf("slow subscriber = %v, want only %d", slowSeqs, first)
	}
	fullSeqs := drainAll(t, full)
	if len(fullSeqs) != 20 || fullSeqs[0] != first || fullSeqs[19] != last {
		t.Fatalf("other subscriber got %d events %v, want all 20", len(fullSeqs), fullSeqs)
	}
	for i := 1; i < len(fullSeqs); i++ {
		if fullSeqs[i] <= fullSeqs[i-1] {
			t.Fatal("sequence numbers not strictly increasing")
		}
	}
	if slow.Stats().Dropped != 19 {
		t.Fatalf("slow Dropped = %d, want 19", slow.Stats().Dropped)
	}
	if full.Stats().Dropped != 0 {
		t.Fatalf("full subscriber Dropped = %d, want 0", full.Stats().Dropped)
	}
}

func TestGapsMapExactlyToDrops(t *testing.T) {
	d := New()
	defer d.Close()
	sub, _ := d.Subscribe("u", nil, Options{Capacity: 3, Policy: DropOldest, DrainOnUnsubscribe: true})
	first, last := publishN(t, d, 30)
	sub.Unsubscribe()
	seqs := drainAll(t, sub)

	var seen = map[int64]bool{}
	var prev int64
	for i, seq := range seqs {
		if i > 0 && seq <= prev {
			t.Fatalf("non-increasing seq %d after %d", seq, prev)
		}
		seen[seq] = true
		prev = seq
	}
	var missing int64
	for seq := first; seq <= last; seq++ {
		if !seen[seq] {
			missing++
		}
	}
	if missing != sub.Stats().Dropped {
		t.Fatalf("missing seqs = %d, Dropped = %d", missing, sub.Stats().Dropped)
	}
}
