package api

import (
	"errors"
	"sync"
	"testing"

	"ontology/evt"
)

func TestNewBadGap(t *testing.T) {
	for _, g := range []int64{0, -1, -100} {
		if w, err := New(g, 0); !errors.Is(err, ErrInvalidGap) || w != nil {
			t.Fatalf("New(%d) = %v, %v; want nil, ErrInvalidGap", g, w, err)
		}
	}
	w, err := New(10, 0)
	if err != nil || w == nil {
		t.Fatalf("New(10) failed: %v", err)
	}
}

func TestSentinelsDistinct(t *testing.T) {
	all := []error{ErrInvalidGap, ErrTooManySessions, ErrInvalidEvent}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if errors.Is(all[i], all[j]) || all[i].Error() == all[j].Error() {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}

func TestFeedAtomicity(t *testing.T) {
	cases := []struct {
		name    string
		max     int
		evs     []evt.Event
		wantErr error
	}{
		{"empty key", 0, []evt.Event{{Key: "k", TS: 1}, {Key: "", TS: 2}}, ErrInvalidEvent},
		{"cap exceeded", 1, []evt.Event{{Key: "k", TS: 100}}, ErrTooManySessions},
		{"bad after good in one batch", 0, []evt.Event{{Key: "a", TS: 1}, {Key: "", TS: 2}}, ErrInvalidEvent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, _ := New(10, c.max)
			if err := w.Feed([]evt.Event{{Key: "k", TS: 0}}); err != nil {
				t.Fatal(err)
			}
			before := w.Snapshot("k")
			if err := w.Feed(c.evs); !errors.Is(err, c.wantErr) {
				t.Fatalf("Feed err = %v, want %v", err, c.wantErr)
			}
			if got := w.Snapshot("k"); len(got) != len(before) { // existing key untouched
				t.Fatalf("key %q changed after rejection: %v", "k", got)
			}
			if got := w.Snapshot("a"); len(got) != 0 { // no partial batch trace
				t.Fatalf("key %q appeared after rejection: %v", "a", got)
			}
			if err := w.Feed([]evt.Event{{Key: "k", TS: 5}}); err != nil { // still usable
				t.Fatalf("window unusable after rejection: %v", err)
			}
			if got := w.Snapshot("k"); len(got) != 1 || got[0].N != 2 || got[0].End != 5 {
				t.Fatalf("post-rejection feed wrong: %v", got)
			}
		})
	}
}

func TestSelfCheck(t *testing.T) {
	w, _ := New(10, 0)
	if err := w.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders: N goroutines only-read one fed window; every
// snapshot must agree field by field. No sleep is used to fabricate timing.
func TestConcurrentReaders(t *testing.T) {
	w, _ := New(10, 0)
	var batch []evt.Event
	for _, ts := range []int64{100, 105, 130, 135, 118, 100, 1, 200, 111, 134, 3, 2} {
		batch = append(batch, evt.Event{Key: "k", TS: ts})
	}
	if err := w.Feed(batch); err != nil {
		t.Fatal(err)
	}
	ref := w.Snapshot("k")

	const N, iters = 32, 100
	var wg sync.WaitGroup
	var failMu sync.Mutex
	failed := false
	fail := func(format string, a ...any) {
		failMu.Lock()
		failed = true
		t.Errorf(format, a...)
		failMu.Unlock()
	}
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for it := 0; it < iters; it++ {
				if id%4 == 0 { // a quarter of readers also run SelfCheck
					if err := w.SelfCheck(); err != nil {
						fail("SelfCheck: %v", err)
						return
					}
				}
				got := w.Snapshot("k")
				if len(got) != len(ref) {
					fail("len mismatch: %d vs %d", len(got), len(ref))
					return
				}
				for i := range ref { // field by field, including counts
					if got[i].Start != ref[i].Start || got[i].End != ref[i].End || got[i].N != ref[i].N {
						fail("session %d mismatch: %+v vs %+v", i, got[i], ref[i])
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if failed {
		t.Fatal("concurrent readers disagreed")
	}
}
