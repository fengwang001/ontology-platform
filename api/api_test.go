package api_test

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sess"
)

func feed(t *testing.T, a *api.API, key string, ts ...int64) {
	t.Helper()
	evs := make([]api.Event, len(ts))
	for i, x := range ts {
		evs[i] = api.Event{Key: key, TS: x}
	}
	if _, err := a.Feed(evs); err != nil {
		t.Fatal(err)
	}
}

// TestViewMatchesBatch pins invariant 1: view == batch recompute on accepted events.
func TestViewMatchesBatch(t *testing.T) {
	cases := [][]int64{
		{10, 13, 20, 16, 23, 17, 25, 11},
		{5, 1, 30, 2, 9, 33, 40, 6},
		{100, 104, 108, 50, 0, 200, 196},
	}
	for ci, ts := range cases {
		a, _ := api.New(3, 10000)
		var accepted []int64
		for _, x := range ts {
			d := a.Dropped()
			if _, err := a.Feed([]api.Event{{Key: "K", TS: x}}); err != nil {
				t.Fatal(err)
			}
			if a.Dropped() == d {
				accepted = append(accepted, x)
			}
		}
		got, want := a.View()["K"], sess.Link(accepted, 3)
		if !slices.EqualFunc(got, want, func(p, q sess.Session) bool {
			return p.Start == q.Start && p.End == q.End && p.Count == q.Count
		}) {
			t.Fatalf("case %d: %+v != batch %+v", ci, got, want)
		}
	}
}

// TestClosureImmutability pins invariant 2: frozen rows never change again.
func TestClosureImmutability(t *testing.T) {
	a, _ := api.New(3, 1000)
	feed(t, a, "K", 10, 13, 20) // closes [10,13]
	frozen := a.View()["K"][0]
	for i, ts := range []int64{16, 11, 12, 1000, 13} {
		feed(t, a, "K", ts)
		if got := a.View()["K"][0]; got != frozen {
			t.Fatalf("post %d: closed row mutated to %+v", i, got)
		}
	}
}

// TestDropConsistency pins invariant 3: Dropped counts exactly the late
// events whose merge set contains a closed session.
func TestDropConsistency(t *testing.T) {
	cases := []struct {
		ts      []int64
		dropped int
	}{
		{[]int64{10, 13, 20, 16, 23, 17, 25, 11}, 2}, // 16 and 11 dropped
		{[]int64{10, 20, 100, 95}, 0},                // late, adjacent to nothing closed
		{[]int64{10, 20, 11}, 1},                     // late into closed neighbourhood
	}
	for _, tc := range cases {
		a, _ := api.New(3, 1000)
		feed(t, a, "K", tc.ts...)
		if a.Dropped() != tc.dropped {
			t.Fatalf("ts=%v: dropped %d, want %d", tc.ts, a.Dropped(), tc.dropped)
		}
	}
}

// TestRejectAtomic pins invariant 4: any rejection leaves zero trace.
func TestRejectAtomic(t *testing.T) {
	a, _ := api.New(3, 1)
	for _, tc := range []struct {
		evs  []api.Event
		want error
	}{
		{[]api.Event{{Key: "A", TS: 1}, {Key: "", TS: 2}}, api.ErrEmptyKey},
		{[]api.Event{{Key: "A", TS: 1}, {Key: "B", TS: 4}}, api.ErrTooManyOpen},
	} {
		if _, err := a.Feed(tc.evs); !errors.Is(err, tc.want) {
			t.Fatalf("err = %v, want %v", err, tc.want)
		}
		if len(a.View()) != 0 || a.Dropped() != 0 {
			t.Fatal("rejected feed left state behind")
		}
	}
	if _, err := api.New(0, 1); !errors.Is(err, api.ErrNonPositiveGap) {
		t.Fatal("non-positive gap accepted")
	}
	if _, err := a.Feed([]api.Event{{Key: "A", TS: 1}}); err != nil {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	a, _ := api.New(3, 10)
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReaders: many goroutines read a fed instance; every view is
// field-identical. No sleeps; the race detector guards concurrency.
func TestConcurrentReaders(t *testing.T) {
	a, _ := api.New(3, 1000)
	feed(t, a, "K", 10, 13, 20, 16, 23, 17, 25, 11)
	feed(t, a, "A", 5, 1, 30, 33)
	const n = 32
	var wg sync.WaitGroup
	res := make([]string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			b, _ := json.Marshal(struct {
				V map[string][]sess.Session
				D int
			}{a.View(), a.Dropped()})
			res[i] = string(b)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if res[i] != res[0] {
			t.Fatalf("reader %d differs", i)
		}
	}
}
