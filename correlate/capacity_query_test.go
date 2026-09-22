package correlate

import (
	"errors"
	"testing"
	"time"
)

func TestCapacityRejectionChangesNothing(t *testing.T) {
	cor, _ := newTestCorrelator(2)
	t0, _ := cor.Issue(time.Minute)
	t1, _ := cor.Issue(time.Minute)

	before := cor.SnapshotState()
	_, err := cor.Issue(time.Minute)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("issue over capacity err = %v, want ErrCapacity", err)
	}
	after := cor.SnapshotState()
	if after != before {
		t.Fatalf("state changed on rejected issue:\nbefore=%+v\nafter =%+v", before, after)
	}
	// Existing slots are untouched and still deliverable.
	if err := cor.Deliver(t0); err != nil {
		t.Fatal(err)
	}
	if err := cor.Deliver(t1); err != nil {
		t.Fatal(err)
	}
}

func TestLookupZeroValueAndStableReads(t *testing.T) {
	cor, clk := newTestCorrelator(2)
	// Never allocated: zero value.
	if st, ok := cor.Lookup(5); ok || st != (Status{}) {
		t.Fatalf("unknown lookup = %+v,%v", st, ok)
	}

	tok, _ := cor.Issue(10 * time.Second)
	st, ok := cor.Lookup(tok.ID)
	if !ok || !st.Waiting || st.Epoch != 1 || st.Remaining != 10*time.Second {
		t.Fatalf("waiting lookup = %+v,%v", st, ok)
	}
	// Two reads at the same clock value are identical and advance nothing.
	again, ok2 := cor.Lookup(tok.ID)
	if !ok2 || again != st {
		t.Fatalf("non-idempotent reads: %+v vs %+v", st, again)
	}

	cor.Deliver(tok)
	if st, ok := cor.Lookup(tok.ID); ok || st != (Status{}) {
		t.Fatalf("finished lookup must be zero: %+v,%v", st, ok)
	}

	// A read after the deadline observes lazy timeout exactly once.
	other, _ := cor.Issue(time.Minute)
	clk.advance(time.Minute)
	if _, ok := cor.Lookup(other.ID); ok {
		t.Fatal("read should observe deadline timeout")
	}
	if got := cor.Counters().TimedOut; got != 1 {
		t.Fatalf("timed out = %d, want 1", got)
	}
	if _, ok := cor.Lookup(other.ID); ok {
		t.Fatal("second read stays terminal")
	}
	if got := cor.Counters().TimedOut; got != 1 {
		t.Fatalf("repeated read advanced state: %d", got)
	}
}

func TestAllocationSequenceIsReproducible(t *testing.T) {
	seq := func() []int {
		cor, clk := newTestCorrelator(4)
		toks := make([]Token, 4)
		for i := range toks {
			toks[i], _ = cor.Issue(time.Minute)
		}
		cor.Deliver(toks[2]) // finish id 2
		cor.Cancel(toks[0])  // free id 0
		clk.advance(time.Minute + time.Second)
		// ids 1 and 3 time out; next allocations reuse smallest first.
		got := []int{}
		for range toks {
			tok, err := cor.Issue(time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, tok.ID)
		}
		return got
	}
	a, b := seq(), seq()
	want := []int{0, 1, 2, 3}
	for i := range want {
		if a[i] != want[i] {
			t.Fatalf("reuse order = %v, want %v", a, want)
		}
		if a[i] != b[i] {
			t.Fatalf("sequence not reproducible: %v vs %v", a, b)
		}
	}
}
