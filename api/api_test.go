package api

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func canonicalPlan() [][]Event {
	return [][]Event{
		{{Key: "k", TS: 10, V: 1}, {Key: "k", TS: 5, V: 2}}, {{Key: "k", TS: 20, V: 3}, {Key: "k", TS: 12, V: 4}},
		{{Key: "k", TS: 8, V: 5}, {Key: "k", TS: 12, V: 6}}, {{Key: "k", TS: 25, V: 7}, {Key: "k", TS: 25, V: 9}},
	}
}

func feedPlan(t *testing.T, plan [][]Event, max int) *V {
	t.Helper()
	v, _ := New(max) // tests only pass valid max and well-formed events
	for _, b := range plan {
		v.BeginBatch()
		for _, e := range b {
			v.Feed(e)
		}
	}
	v.Flush()
	return v
}

// Invariant 1: online equals batch recompute for random batches/orders.
func TestInvariantBatchRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	keys := []string{"a", "b", "c"}
	for trial := 0; trial < 200; trial++ {
		var plan [][]Event
		for nb := 1 + rng.Intn(4); nb > 0; nb-- {
			var b []Event
			for ne := rng.Intn(6); ne > 0; ne-- {
				b = append(b, Event{Key: keys[rng.Intn(3)], TS: rng.Int63n(30), V: rng.Intn(100)})
			}
			plan = append(plan, b)
		}
		for _, b := range plan {
			rng.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
		}
		v := feedPlan(t, plan, 1000)
		rv, ra, rd := recompute(plan)
		if v.Dropped() != rd || !reflect.DeepEqual(v.AlignedTimes(), ra) {
			t.Fatalf("trial %d: dropped=%d/%d aligned=%v/%v", trial, v.Dropped(), rd, v.AlignedTimes(), ra)
		}
		for k, e := range rv {
			if got, ok := v.Value(k); !ok || got != e {
				t.Fatalf("trial %d: value(%q)=%d,%v ref=%d", trial, k, got, ok, e)
			}
		}
	}
	base := []Event{{Key: "k", TS: 3, V: 1}, {Key: "k", TS: 1, V: 2}, {Key: "k", TS: 2, V: 3}}
	for _, p := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}} {
		w := feedPlan(t, [][]Event{{{Key: "x", TS: 0}}, {base[p[0]], base[p[1]], base[p[2]]}}, 8)
		if got, _ := w.Value("k"); got != 1 {
			t.Fatalf("order %v: value=%d want 1", p, got)
		}
	}
}

// Invariant 2: aligned times non-decreasing, each the per-batch min accepted TS.
func TestInvariantAlignedTimesMonotonic(t *testing.T) {
	cases := []struct {
		plan [][]Event
		want []int64
		drop int
	}{
		{canonicalPlan(), []int64{5, 12, 12, 25}, 1},
		{[][]Event{{}, {{Key: "k", TS: 20, V: 1}, {Key: "k", TS: 12, V: 2}}}, []int64{noBound, 12}, 0},
		{[][]Event{{{Key: "k", TS: 5, V: 1}}, {}, {{Key: "k", TS: 100, V: 2}}}, []int64{5, 5, 100}, 0},
		{[][]Event{{{Key: "k", TS: 5, V: 1}}, {{Key: "k", TS: 4, V: 2}}}, []int64{5, 5}, 1},
	}
	for i, c := range cases {
		v := feedPlan(t, c.plan, 100)
		if !reflect.DeepEqual(v.AlignedTimes(), c.want) || v.Dropped() != c.drop {
			t.Fatalf("case %d: aligned=%v dropped=%d, want %v/%d", i, v.AlignedTimes(), v.Dropped(), c.want, c.drop)
		}
	}
}

// Invariant 3: dropped events touch neither values nor aligned times.
func TestInvariantDroppedSemantics(t *testing.T) {
	w := feedPlan(t, [][]Event{
		{{Key: "k", TS: 5, V: 1}},
		{{Key: "k", TS: 4, V: 999}, {Key: "k", TS: 6, V: 2}},
	}, 8)
	got, _ := w.Value("k")
	if w.Dropped() != 1 || got != 2 || !reflect.DeepEqual(w.AlignedTimes(), []int64{5, 6}) {
		t.Fatalf("dropped=%d value=%d aligned=%v, want 1/2/[5 6]", w.Dropped(), got, w.AlignedTimes())
	}
}

// Invariant 4: four distinct sentinels; rejections leave no trace.
func TestInvariantRejectionLeavesNoTrace(t *testing.T) {
	seen := map[error]bool{}
	for _, e := range []error{ErrInvalidParam, ErrInvalidEvent, ErrNoOpenBatch, ErrBatchTooLarge} {
		if seen[e] {
			t.Fatal("sentinel errors not distinct")
		}
		seen[e] = true
	}
	_, errParam := New(0)
	v, _ := New(1)
	errNoBatch := v.Feed(Event{Key: "k", TS: 1})
	v.BeginBatch()
	errKey := v.Feed(Event{Key: "", TS: 1})
	errOK := v.Feed(Event{Key: "k", TS: 5, V: 1})
	errCap := v.Feed(Event{Key: "k", TS: 6, V: 2})
	if errParam != ErrInvalidParam || errNoBatch != ErrNoOpenBatch || errKey != ErrInvalidEvent || errOK != nil || errCap != ErrBatchTooLarge {
		t.Fatalf("rejections: %v %v %v %v %v", errParam, errNoBatch, errKey, errOK, errCap)
	}
	if got, _ := v.Value("k"); got != 1 || v.Dropped() != 0 || len(v.AlignedTimes()) != 0 {
		t.Fatal("state changed after rejections")
	}
	v.Flush()
	if at := v.AlignedTimes(); !reflect.DeepEqual(at, []int64{5}) {
		t.Fatalf("instance not usable after rejections: aligned=%v", at)
	}
}

// Concurrent read-only access: N goroutines observe field-identical
// state. No sleeps.
func TestConcurrentReaders(t *testing.T) {
	v := feedPlan(t, canonicalPlan(), 8)
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, _ := v.Value("k")
				if got != 9 || v.Dropped() != 1 || !reflect.DeepEqual(v.AlignedTimes(), []int64{5, 12, 12, 25}) || v.SelfCheck() != nil {
					errs <- "mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
