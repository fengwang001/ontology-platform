package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/aln"
	"ontology/api"
)

// build feeds a deterministic scenario; returns instance + brute-force expectations.
func build(t *testing.T, seed int64, maxPer int) (api.V, map[string]int, []int64, int) {
	v, _ := api.New(maxPer)
	s := uint64(seed)
	rnd := func(n int64) int64 { s = s*6364136223846793005 + 1442695040888963407; return int64(s>>33) % n }
	acc, mins, dropped, prev, hasPrev := map[string][]api.Ev{}, []int64{}, 0, aln.None, false
	for b := 0; b < 2+int(rnd(4)); b++ {
		v.BeginBatch()
		cur, nAcc := aln.None, 0
		for i, n := int64(0), rnd(int64(maxPer+3)); i < n; i++ {
			e := api.Ev{Key: string(rune('a' + rnd(3))), TS: rnd(50), V: int(rnd(97))}
			err := v.Feed(e)
			if err != nil && !errors.Is(err, api.ErrBatchFull) {
				t.Fatalf("feed: %v", err)
			}
			if err == nil && hasPrev && e.TS < prev {
				dropped++
			} else if err == nil {
				acc[e.Key] = append(acc[e.Key], e)
				cur, nAcc = min(cur, e.TS), nAcc+1
			}
		}
		v.EndBatch()
		if nAcc > 0 {
			prev, hasPrev = cur, true
		}
		mins = append(mins, prev)
	}
	v.Flush()
	want := map[string]int{}
	for k, evs := range acc {
		best := evs[0]
		for _, e := range evs[1:] {
			if e.TS >= best.TS {
				best = e
			}
		}
		want[k] = best.V
	}
	return v, want, mins, dropped
}

func TestInvariant1BatchRecompute(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		v, want, _, _ := build(t, seed, 4)
		for k, w := range want {
			if got, ok := v.Value(k); !ok || got != w {
				t.Errorf("seed=%d %s: got %d,%v want %d", seed, k, got, ok, w)
			}
		}
	}
}

func TestInvariant2AlignedMonotone(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		v, _, mins, _ := build(t, seed, 4)
		if got := v.AlignedTimes(); !slices.Equal(got, mins) {
			t.Errorf("seed=%d: aligned %v want %v", seed, got, mins)
		}
	}
}

func TestInvariant3DroppedConsistent(t *testing.T) {
	total := 0
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		v, _, _, dropped := build(t, seed, 4)
		if v.Dropped() != dropped {
			t.Errorf("seed=%d: dropped %d want %d", seed, v.Dropped(), dropped)
		}
		total += dropped
	}
	if total == 0 {
		t.Error("no late event exercised")
	}
}

func TestInvariant4NoStateOnReject(t *testing.T) {
	errs := []error{api.ErrNonPositiveMax, api.ErrEmptyKey, api.ErrNoOpenBatch, api.ErrBatchFull}
	for i, e := range errs {
		if slices.Contains(errs[:i], e) {
			t.Fatalf("sentinels not distinct: %v", e)
		}
	}
	v, _ := api.New(2)
	if _, err := api.New(0); !errors.Is(err, api.ErrNonPositiveMax) {
		t.Fatal("New(0)")
	}
	if err := v.Feed(api.Ev{Key: "k", TS: 1}); !errors.Is(err, api.ErrNoOpenBatch) {
		t.Fatal("no open batch")
	}
	v.BeginBatch()
	for _, e := range []api.Ev{{Key: "k", TS: 5, V: 7}, {Key: "k", TS: 6, V: 8}} {
		if err := v.Feed(e); err != nil {
			t.Fatalf("valid feed: %v", err)
		}
	}
	al0, dr0 := v.AlignedTimes(), v.Dropped()
	val0, _ := v.Value("k")
	if err := v.Feed(api.Ev{TS: 9}); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatal("empty key")
	}
	if err := v.Feed(api.Ev{Key: "k", TS: 7}); !errors.Is(err, api.ErrBatchFull) {
		t.Fatal("batch full")
	}
	if val, _ := v.Value("k"); !slices.Equal(v.AlignedTimes(), al0) || v.Dropped() != dr0 || val != val0 {
		t.Fatal("rejected ops changed state")
	}
	v.EndBatch()
	v.BeginBatch()
	if err := v.Feed(api.Ev{Key: "k", TS: 10, V: 9}); err != nil {
		t.Fatal("unusable after rejections")
	}
}

// TestConcurrentReads: 16 goroutines read one filled instance; identical results, no sleeps.
func TestConcurrentReads(t *testing.T) {
	v, want, mins, dropped := build(t, 99, 4)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				for k, w := range want {
					if got, _ := v.Value(k); got != w {
						t.Errorf("value %s=%d want %d", k, got, w)
						return
					}
				}
				if !slices.Equal(v.AlignedTimes(), mins) || v.Dropped() != dropped || v.SelfCheck() != nil {
					t.Error("read-only result changed")
					return
				}
			}
		}()
	}
	wg.Wait()
}
