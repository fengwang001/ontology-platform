package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/agg"
	"ontology/delta"
)

func oracle(a map[int64]int64) (s Snap) {
	for v, n := range a {
		s.Count, s.Sum = s.Count+n, s.Sum+v*n
		if s.Present {
			s.Min, s.Max = min(s.Min, v), max(s.Max, v)
		} else {
			s.Min, s.Max, s.MinOK, s.MaxOK, s.Present = v, v, true, true, true
		}
	}
	return
}

// TestFeedMatchesRecompute: random multi-group streams, after each event the view must equal a from-scratch recomputation.
func TestFeedMatchesRecompute(t *testing.T) {
	for _, seed := range []int64{11, 22} {
		v, rng := New(8), rand.New(rand.NewSource(seed))
		live := map[string]map[int64]int64{}
		for _, k := range []string{"a", "b", "c"} {
			live[k] = map[int64]int64{}
		}
		for i := 0; i < 3000; i++ {
			ks := []string{"a", "b", "c"}
			k := ks[rng.Intn(3)]
			val, op := int64(rng.Intn(19))-9, delta.Insert
			if len(live[k]) > 0 && rng.Intn(2) == 0 {
				op = delta.Retract
				for val = range live[k] {
					break
				}
			}
			if err := v.Feed([]delta.Event{{Key: k, Val: val, Op: op}}); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, i, err)
			}
			if op == delta.Insert {
				live[k][val]++
			} else if live[k][val]--; live[k][val] == 0 {
				delete(live[k], val)
			}
			got, present := v.Snapshot(k)
			if want := oracle(live[k]); present != want.Present || present && got != want {
				t.Fatalf("seed %d step %d key %s: %+v(%v) != %+v", seed, i, k, got, present, want)
			}
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := New(16).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestRejectedEventsLeaveNoTrace: three distinct failures + a mid-batch failure leave the view unchanged and still usable.
func TestRejectedEventsLeaveNoTrace(t *testing.T) {
	setup := func(evs ...delta.Event) func(*View) {
		return func(v *View) { _ = v.Feed(evs) }
	}
	act := func(evs ...delta.Event) func(*View) error {
		return func(v *View) error { return v.Feed(evs) }
	}
	ins := func(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Insert} }
	gone := func(k string, x int64) delta.Event { return delta.Event{Key: k, Val: x, Op: delta.Retract} }
	cases := []struct {
		name  string
		setup func(*View)
		act   func(*View) error
		want  error
	}{
		{"retract missing", nil, act(gone("g", 1)), delta.ErrRetractMissing},
		{"too many groups", setup(ins("a", 1)), act(ins("b", 1)), ErrTooManyGroups},
		{"sum overflow", setup(ins("g", math.MaxInt64)), act(ins("g", 1)), agg.ErrSumOverflow},
		{"mid-batch rollback", setup(ins("g", 3), ins("g", 4)), act(ins("g", 1), gone("g", 9)), delta.ErrRetractMissing},
	}
	if delta.ErrRetractMissing == ErrTooManyGroups || ErrTooManyGroups == agg.ErrSumOverflow ||
		delta.ErrRetractMissing == agg.ErrSumOverflow {
		t.Fatal("the three sentinel errors must be distinct")
	}
	for _, tc := range cases {
		v := New(1)
		if tc.setup != nil {
			tc.setup(v)
		}
		before, _ := v.Snapshot("g")
		if err := tc.act(v); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v want %v", tc.name, err, tc.want)
		}
		if after, _ := v.Snapshot("g"); after != before {
			t.Fatalf("%s left a trace: %+v != %+v", tc.name, before, after)
		}
	}
	u := New(1)
	if err := u.Feed([]delta.Event{gone("g", 1)}); !errors.Is(err, delta.ErrRetractMissing) {
		t.Fatalf("setup reject: %v", err)
	}
	if err := u.Feed([]delta.Event{ins("g", 42)}); err != nil {
		t.Fatalf("view unusable after rejection: %v", err)
	}
	if s, ok := u.Snapshot("g"); !ok || s.Count != 1 || s.Sum != 42 {
		t.Fatalf("post-rejection feed wrong: %+v", s)
	}
}

// TestConcurrentSnapshots: many goroutines read a fed view concurrently and
// every reader observes the identical tuple. No sleeps are used.
func TestConcurrentSnapshots(t *testing.T) {
	v := New(8)
	var evs []delta.Event
	for _, x := range []int64{2, 8, -5, 8, 2} {
		evs = append(evs, delta.Event{Key: "k", Val: x, Op: delta.Insert})
	}
	if err := v.Feed(evs); err != nil {
		t.Fatal(err)
	}
	want, _ := v.Snapshot("k")
	const n, rounds = 64, 50
	var wg sync.WaitGroup
	bad := false
	var lk sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				got, ok := v.Snapshot("k")
				lk.Lock()
				if !ok || got != want {
					bad = true
				}
				lk.Unlock()
			}
		}()
	}
	wg.Wait()
	if bad {
		t.Fatalf("concurrent readers diverged; want %+v", want)
	}
}
