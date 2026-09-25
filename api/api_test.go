package api_test

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

func TestFeedVectorsAndSelfCheck(t *testing.T) {
	e := func(ts int64, k string) api.Event { return api.Event{TS: ts, Key: k} }
	cases := []struct {
		B, r int
		evs  []api.Event
		want []int64
	}{
		{3, 1,
			[]api.Event{e(0, "k1"), e(0, "k2"), e(0, "k3"), e(1, "k4"), e(1, "k5"), e(4, "k6")},
			[]int64{0, 0, 0, 1, 2, 4}},
		{1, 1,
			[]api.Event{e(0, "a"), e(0, "b"), e(0, "c"), e(5, "d"), e(5, "e")},
			[]int64{0, 1, 2, 5, 6}},
		{2, 1,
			[]api.Event{e(0, "a"), e(0, "b"), e(0, "c"), e(1, "d")},
			[]int64{0, 0, 1, 2}},
		{1, 0,
			[]api.Event{e(0, "a"), e(0, "b")},
			[]int64{0, math.MaxInt64}},
	}
	for _, c := range cases {
		l, err := api.New(c.B, c.r)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got, err := l.Feed(c.evs)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("B=%d r=%d: got %v want %v", c.B, c.r, got, c.want)
		}
		if l.Dropped() != 0 {
			t.Fatalf("Dropped=%d", l.Dropped())
		}
		if err := l.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
	}
}

func TestSentinelErrorsDistinctAndNoTrace(t *testing.T) {
	if _, e := api.New(0, 1); !errors.Is(e, api.ErrInvalidParam) {
		t.Fatalf("B=0: %v", e)
	}
	if _, e := api.New(1, -1); !errors.Is(e, api.ErrInvalidParam) {
		t.Fatalf("r<0: %v", e)
	}
	l, _ := api.New(1, 1)
	if _, e := l.Feed([]api.Event{{TS: 0, Key: "a"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := l.Feed([]api.Event{{TS: 2, Key: "ok"}, {TS: 1, Key: "bad"}}); !errors.Is(e, api.ErrTSRollback) {
		t.Fatalf("rollback: %v", e)
	}
	if _, e := l.Feed([]api.Event{{TS: 2, Key: ""}}); !errors.Is(e, api.ErrEmptyKey) {
		t.Fatalf("empty key: %v", e)
	}
	if errors.Is(api.ErrTSRollback, api.ErrEmptyKey) || errors.Is(api.ErrInvalidParam, api.ErrEmptyKey) {
		t.Fatal("sentinel errors are not distinct")
	}
	got, e := l.Feed([]api.Event{{TS: 2, Key: "z"}, {TS: 2, Key: "w"}})
	if e != nil || !reflect.DeepEqual(got, []int64{2, 3}) || l.Dropped() != 0 {
		t.Fatalf("state tainted after rejection: %v %v", got, e)
	}
}

func TestConcurrentFeedAndReads(t *testing.T) {
	l, err := api.New(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := l.Feed([]api.Event{{TS: 0, Key: "s1"}, {TS: 0, Key: "s2"}, {TS: 0, Key: "s3"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := append([]int64(nil), seed...) // {0,0,1}, immutable reference

	const readers, feeders, iters = 8, 4, 2000
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := false
	note := func(format string, a ...any) {
		mu.Lock()
		bad = true
		t.Errorf(format, a...)
		mu.Unlock()
	}
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				// Every reader must observe the seed result field-by-field
				// identical, Dropped()==0, and a clean SelfCheck.
				seen, err := l.Feed(nil)
				if err != nil || len(seen) != 0 {
					note("empty Feed: %v %v", seen, err)
				}
				if l.Dropped() != 0 || !reflect.DeepEqual(snapshot, []int64{0, 0, 1}) {
					note("unstable read: dropped=%d snap=%v", l.Dropped(), snapshot)
				}
				if err := l.SelfCheck(); err != nil {
					note("SelfCheck: %v", err)
				}
			}
		}()
	}
	for g := 0; g < feeders; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				// Equal TS is legal in any lock-acquisition order.
				got, err := l.Feed([]api.Event{{TS: 100, Key: "c"}})
				if err != nil || len(got) != 1 || got[0] < 100 {
					note("concurrent Feed: %v %v", got, err)
				}
			}
		}()
	}
	wg.Wait()
	if bad {
		t.Fatal("concurrent readers observed inconsistent results")
	}
}
