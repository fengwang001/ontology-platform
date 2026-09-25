package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// TestSelfCheck exercises the exported invariant self-check.
func TestSelfCheck(t *testing.T) {
	w, err := api.New(3, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestPublicBoundary checks the closed floor and non-retreat via the API.
func TestPublicBoundary(t *testing.T) {
	w, _ := api.New(3, 1)
	steps := []struct{ seq, high, acc, drop int64 }{
		{10, 10, 1, 0}, {8, 10, 2, 0}, {12, 12, 3, 0}, {5, 12, 3, 1},
		{8, 12, 3, 2}, {11, 12, 4, 2}, {9, 12, 5, 2}, {7, 12, 5, 3},
	}
	for i, s := range steps {
		if err := w.Feed([]api.Event{{Key: "a", Seq: s.seq}}); err != nil {
			t.Fatal(err)
		}
		h, ok := w.High("a")
		if !ok || h != s.high || w.Accepted("a") != s.acc || w.Dropped() != s.drop {
			t.Fatalf("step %d ok=%v high=%d acc=%d drop=%d", i, ok, h, w.Accepted("a"), w.Dropped())
		}
	}
	// Duplicate (key,seq) deliveries are independent events, no dedup.
	w2, _ := api.New(3, 1)
	if err := w2.Feed([]api.Event{{Key: "a", Seq: 5}, {Key: "a", Seq: 5}}); err != nil {
		t.Fatal(err)
	}
	if w2.Accepted("a") != 2 {
		t.Fatalf("duplicate delivery: acc=%d want 2", w2.Accepted("a"))
	}
	// Unknown key: zero accepted and High reports not-found.
	if w2.Accepted("nope") != 0 {
		t.Fatal("unknown key accepted must be 0")
	}
	if h, ok := w2.High("nope"); ok || h != 0 {
		t.Fatalf("unknown key high=(%d,%v)", h, ok)
	}
}

// TestErrorsDistinctAndAtomic: three decidable errors; rejected batch leaves
// the counter usable with unchanged state.
func TestErrorsDistinctAndAtomic(t *testing.T) {
	if _, err := api.New(-1, 1); !errors.Is(err, api.ErrInvalidArgs) {
		t.Fatalf("K<0: %v", err)
	}
	if _, err := api.New(0, 0); !errors.Is(err, api.ErrInvalidArgs) {
		t.Fatalf("maxKeys=0: %v", err)
	}
	if errors.Is(api.ErrEmptyKey, api.ErrTooManyKeys) ||
		errors.Is(api.ErrTooManyKeys, api.ErrInvalidArgs) ||
		errors.Is(api.ErrEmptyKey, api.ErrInvalidArgs) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	w, _ := api.New(3, 1)
	if err := w.Feed([]api.Event{{Key: "x", Seq: 1}}); err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		evs []api.Event
		err error
	}{
		{[]api.Event{{Key: "", Seq: 1}}, api.ErrEmptyKey},
		{[]api.Event{{Key: "y", Seq: 1}}, api.ErrTooManyKeys},
	} {
		if err := w.Feed(tc.evs); !errors.Is(err, tc.err) {
			t.Fatalf("case %d err=%v want %v", i, err, tc.err)
		}
		if w.Accepted("x") != 1 || w.Dropped() != 0 {
			t.Fatalf("case %d: rejected batch left a trace", i)
		}
	}
	if err := w.Feed([]api.Event{{Key: "x", Seq: 4}}); err != nil || w.Accepted("x") != 2 {
		t.Fatalf("counter unusable after rejection: err=%v acc=%d", err, w.Accepted("x"))
	}
}

// TestConcurrentFeed: disjoint-key Feeds equal serial; readers identical. No sleeps; run with -race.
func TestConcurrentFeed(t *testing.T) {
	const N = 32
	w, _ := api.New(3, N*2)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := fmt.Sprintf("p%02d", g)
			err := w.Feed([]api.Event{
				{Key: k, Seq: int64(g) + 2},
				{Key: k, Seq: int64(g)},
				{Key: k, Seq: int64(g) + 5},
			})
			if err != nil {
				t.Error(err)
			}
		}(g)
	}

	filled, _ := api.New(3, N)
	rb := make([]api.Event, N)
	for i := range rb {
		rb[i] = api.Event{Key: fmt.Sprintf("r%02d", i), Seq: int64(i + 1)}
	}
	if err := filled.Feed(rb); err != nil {
		t.Fatal(err)
	}
	snaps := make([][][2]int64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			snaps[g] = make([][2]int64, N)
			for i := 0; i < N; i++ {
				k := fmt.Sprintf("r%02d", i)
				h, _ := filled.High(k)
				snaps[g][i] = [2]int64{h, filled.Accepted(k)}
			}
		}(g)
	}
	wg.Wait()

	for g := 0; g < N; g++ { // serial expectation: disjoint keys, all 3 accepted
		k := fmt.Sprintf("p%02d", g)
		h, ok := w.High(k)
		if !ok || h != int64(g)+5 || w.Accepted(k) != 3 {
			t.Fatalf("key %s high=%d acc=%d not serial-equivalent", k, h, w.Accepted(k))
		}
		for i := range snaps[g] {
			if snaps[g][i] != snaps[0][i] {
				t.Fatalf("reader %d key %d: %v != %v", g, i, snaps[g][i], snaps[0][i])
			}
		}
	}
}
