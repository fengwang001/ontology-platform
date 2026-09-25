package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func build(t *testing.T) *api.Log {
	t.Helper()
	l, err := api.New(3)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 6; i++ {
		if err := l.Append(i, i*10); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// TestErrorsDistinct: the three faults map to three pairwise-distinct
// sentinel errors.
func TestErrorsDistinct(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadSegSize) {
		t.Errorf("New(0) err=%v, want ErrBadSegSize", err)
	}
	l := build(t)
	if err := l.Append(8, 80); !errors.Is(err, api.ErrSeqGap) {
		t.Errorf("Append(8) err=%v, want ErrSeqGap", err)
	}
	for _, r := range [][2]int64{{0, 3}, {4, 2}, {1, 7}} {
		if _, _, err := l.Verify(r[0], r[1]); !errors.Is(err, api.ErrBadRange) {
			t.Errorf("Verify(%d,%d) err=%v, want ErrBadRange", r[0], r[1], err)
		}
	}
	pairs := [][2]error{
		{api.ErrBadSegSize, api.ErrSeqGap},
		{api.ErrSeqGap, api.ErrBadRange},
		{api.ErrBadSegSize, api.ErrBadRange},
	}
	for _, p := range pairs {
		if errors.Is(p[0], p[1]) {
			t.Errorf("sentinels %v and %v must be distinct", p[0], p[1])
		}
	}
}

// TestFailureLeavesNoTrace pins invariant 4: rejected operations change
// neither records, segment sums, nor the total, and the log stays usable.
func TestFailureLeavesNoTrace(t *testing.T) {
	l := build(t)
	before := l.Total()
	s1, err := l.SegSum(1)
	if err != nil {
		t.Fatal(err)
	}
	badOps := []func() error{
		func() error { return l.Append(9, 90) },              // seq gap
		func() error { _, _, e := l.Verify(0, 2); return e }, // from < 1
		func() error { _, _, e := l.Verify(3, 2); return e }, // from > to
		func() error { _, _, e := l.Verify(1, 7); return e }, // to > n
		func() error { _, _, e := l.Recompute(2, 1); return e },
	}
	for i, op := range badOps {
		if op() == nil {
			t.Errorf("bad op %d unexpectedly succeeded", i)
		}
	}
	s1After, err := l.SegSum(1)
	if err != nil || s1After != s1 || l.Total() != before {
		t.Fatalf("state changed: seg1 %d->%d total %d->%d", s1, s1After, before, l.Total())
	}
	if err := l.Append(7, 70); err != nil || l.Total() != before+770 {
		t.Fatalf("log unusable after rejections: err=%v total=%d", err, l.Total())
	}
}

// TestConcurrentVerify: N goroutines verify the same range of a log with
// one corrupted record; all must report the identical corrupt Seq.
func TestConcurrentVerify(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		l := build(t)
		if err := l.Corrupt(4, 70); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		res := make(chan int64, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, ok, err := l.Verify(1, 6)
				if err != nil || ok {
					got = -1
				}
				res <- got
			}()
		}
		wg.Wait()
		close(res)
		for got := range res {
			if got != 4 {
				t.Fatalf("N=%d: goroutine reported corrupt seq %d, want 4", n, got)
			}
		}
	}
}

// TestSelfCheck runs the built-in drill of the four invariants.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
