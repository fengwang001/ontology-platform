package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/log"
)

// TestAppendSequence pins invariant 1 with the seven requests from NOTES.md.
func TestAppendSequence(t *testing.T) {
	steps := []struct {
		ts   int64
		who  string
		op   string
		want int64 // -1 means reject
	}{
		{10, "A", "put", 1}, {12, "A", "get", 2}, {12, "B", "del", 3},
		{9, "A", "put", -1}, {15, "B", "put", 4}, {15, "", "get", -1},
		{20, "A", "del", 5},
	}
	wantLens := []int{2, 3, 4, 4, 5, 5, 6}
	a := api.New()
	for i, s := range steps {
		seq, err := a.Append(s.ts, s.who, s.op)
		if s.want < 0 && err == nil {
			t.Fatalf("step %d: want rejection, got seq=%d", i, seq)
		}
		if s.want >= 0 && (err != nil || seq != s.want) {
			t.Fatalf("step %d: got seq=%d err=%v, want seq=%d", i, seq, err, s.want)
		}
		if n := len(a.Entries()); n != wantLens[i] {
			t.Fatalf("step %d: %d entries, want %d", i, n, wantLens[i])
		}
	}
	for i, e := range a.Entries() { // Seq strictly continuous from 0
		if e.Seq != int64(i) {
			t.Fatalf("entry %d has Seq=%d", i, e.Seq)
		}
	}
}

// TestRejectNoTrace pins invariant 4: every rejection leaves state untouched.
func TestRejectNoTrace(t *testing.T) {
	cases := []struct {
		ts   int64
		who  string
		op   string
		want error
	}{
		{-1, "A", "put", log.ErrNegativeTS},
		{40, "", "put", log.ErrEmptyWho},
		{40, "A", "", log.ErrEmptyOp},
		{25, "A", "put", log.ErrOutOfOrder},
	}
	for _, c := range cases {
		a := api.New()
		for _, ts := range []int64{10, 20, 30} {
			if _, err := a.Append(ts, "u", "op"); err != nil {
				t.Fatal(err)
			}
		}
		before := a.Entries()
		if _, err := a.Append(c.ts, c.who, c.op); !errors.Is(err, c.want) {
			t.Fatalf("append(%d,%q,%q): err=%v, want %v", c.ts, c.who, c.op, err, c.want)
		}
		after := a.Entries()
		if len(after) != len(before) || after[len(after)-1].Hash != before[len(before)-1].Hash {
			t.Fatalf("%v: state changed after rejection", c.want)
		}
		if seq, err := a.Append(40, "u", "op"); err != nil || seq != 4 {
			t.Fatalf("%v: next append seq=%d err=%v, want 4", c.want, seq, err)
		}
	}
}

// TestErrorsDistinct: the four sentinel errors are pairwise distinguishable.
func TestErrorsDistinct(t *testing.T) {
	errs := []error{log.ErrNegativeTS, log.ErrEmptyWho, log.ErrEmptyOp, log.ErrOutOfOrder}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d and %d indistinguishable", i, j)
			}
		}
	}
}

// TestConcurrentReaders: N goroutines get identical Verify/Affected results.
func TestConcurrentReaders(t *testing.T) {
	a := api.New()
	for i := int64(0); i < 200; i++ {
		if _, err := a.Append(i, "u", "op"); err != nil {
			t.Fatal(err)
		}
	}
	wantV, wantA := a.Verify(), a.Affected(150)
	start := make(chan struct{})
	bad := make(chan string, 128)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a.Verify() != wantV {
				bad <- "Verify mismatch"
			}
			if !slices.Equal(a.Affected(150), wantA) {
				bad <- "Affected mismatch"
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}
}

// TestSelfCheck: the built-in self-check passes on a fresh log.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
