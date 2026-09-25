package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/tbucket"
)

func TestEightSteps(t *testing.T) {
	steps := []struct{ ts, bucket, count, drop int64 }{
		{5, 0, 1, 0}, {-12, -2, 1, 0}, {0, 0, 2, 0}, {10, 1, 1, 1},
		{-1, -1, 1, 1}, {20, 2, 1, 2}, {9, 0, 3, 2}, {30, 3, 1, 5},
	}
	m := feed(t, 10, 3, nil)
	for i, s := range steps {
		if err := m.Feed([]api.Event{{TS: s.ts, Key: "k"}}); err != nil {
			t.Fatal(err)
		}
		if v := m.View()["k"]; v[s.bucket] != s.count || m.Dropped() != s.drop {
			t.Fatalf("step %d: count=%d dropped=%d", i+1, v[s.bucket], m.Dropped())
		}
	}
	if v := m.View()["k"]; len(v) != 3 || v[1] != 1 || v[2] != 1 || v[3] != 1 {
		t.Fatalf("final view %v", v)
	}
}

// recompute is the batch formulation of invariant 1.
func recompute(evs []api.Event, size, r int64) map[string]map[int64]int64 {
	out, cur, has := map[string]map[int64]int64{}, int64(0), false
	for _, e := range evs {
		if k := tbucket.Key(e.TS, size); !has || k > cur {
			cur, has = k, true
		}
	}
	for _, e := range evs {
		if k := tbucket.Key(e.TS, size); k >= cur-r+1 {
			if out[e.Key] == nil {
				out[e.Key] = map[int64]int64{}
			}
			out[e.Key][k]++
		}
	}
	return out
}

func feed(t *testing.T, size, r int64, evs []api.Event) *api.Mat {
	t.Helper()
	m, err := api.New(size, r)
	if err != nil || m.Feed(evs) != nil {
		t.Fatal(err)
	}
	return m
}

func randEvents(rng *rand.Rand, n, span int) []api.Event {
	evs := make([]api.Event, n)
	for i := range evs {
		evs[i] = api.Event{TS: int64(rng.Intn(span)) - int64(span/2), Key: string(rune('a' + rng.Intn(3)))}
	}
	return evs
}

func TestBatchRecomputeConsistency(t *testing.T) {
	for _, cfg := range []struct{ size, r int64 }{{10, 3}, {7, 4}, {1, 1}} {
		for seed := int64(0); seed < 5; seed++ {
			evs := randEvents(rand.New(rand.NewSource(seed)), 200, 400)
			if m := feed(t, cfg.size, cfg.r, evs); !reflect.DeepEqual(m.View(), recompute(evs, cfg.size, cfg.r)) {
				t.Fatalf("cfg=%+v seed=%d: view != batch recompute", cfg, seed)
			}
		}
	}
}

func TestRetentionWindow(t *testing.T) {
	for _, cfg := range []struct{ size, r int64 }{{10, 3}, {5, 1}, {100, 7}} {
		m := feed(t, cfg.size, cfg.r, randEvents(rand.New(rand.NewSource(1)), 300, 2000))
		set, maxB := map[int64]bool{}, int64(-1)<<62
		for _, perKey := range m.View() {
			for b := range perKey {
				set[b] = true
				if b > maxB {
					maxB = b
				}
			}
		}
		for b := range set {
			if len(set) > int(cfg.r) || b < maxB-cfg.r+1 {
				t.Fatalf("cfg=%+v: bucket %d outside window, |buckets|=%d", cfg, b, len(set))
			}
		}
	}
}

func TestRejections(t *testing.T) {
	if _, err := api.New(0, 3); !errors.Is(err, api.ErrNonPositiveSize) {
		t.Fatal("size<=0 not rejected")
	}
	if _, err := api.New(10, -1); !errors.Is(err, api.ErrNonPositiveR) {
		t.Fatal("R<=0 not rejected")
	}
	m := feed(t, 10, 3, []api.Event{{TS: 5, Key: "k"}, {TS: 30, Key: "k"}})
	snap, drop := m.View(), m.Dropped()
	err := m.Feed([]api.Event{{TS: 40, Key: "ok"}, {TS: 50, Key: ""}})
	if !errors.Is(err, api.ErrEmptyKey) || errors.Is(err, api.ErrNonPositiveSize) ||
		errors.Is(err, api.ErrNonPositiveR) || api.ErrNonPositiveSize == api.ErrNonPositiveR {
		t.Fatal("sentinel errors not distinguishable")
	}
	if !reflect.DeepEqual(m.View(), snap) || m.Dropped() != drop {
		t.Fatal("rejected batch left a trace")
	}
	if m.Feed([]api.Event{{TS: 31, Key: "k"}}) != nil || api.SelfCheck() != nil {
		t.Fatal("not usable after rejection / SelfCheck failed")
	}
}

func TestConcurrentReads(t *testing.T) {
	m := feed(t, 10, 3, []api.Event{{TS: 5, Key: "a"}, {TS: 30, Key: "b"}, {TS: 9, Key: "a"}})
	want, wantDrop := m.View(), m.Dropped()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if !reflect.DeepEqual(m.View(), want) || m.Dropped() != wantDrop || api.SelfCheck() != nil {
					bad.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() > 0 {
		t.Fatal("divergent concurrent read")
	}
}
